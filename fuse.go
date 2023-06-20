package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

type iCloudInode struct {
	fs.Inode

	node      *iCloudNode
	drive     iCloudDrive
	data      []byte
	dataDirty bool
}

func (inode *iCloudInode) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Size = inode.node.Size
	out.Attr.Mode = 0644
	return 0
}

var _ = (fs.NodeReaddirer)((*iCloudInode)(nil))

func (inode *iCloudInode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	children, err := inode.drive.GetChildren(inode.node)
	if err != nil {
		log.Println("Error:", err)
		// TODO: Probably wrong Errno here :/
		return nil, 1
	}
	for _, node := range *children {
		if node.Filename() == name {
			return inode.generateInode(ctx, &node), 0
		}
	}
	return nil, 0
}

func (inode *iCloudInode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	children, err := inode.drive.GetChildren(inode.node)
	if err != nil {
		log.Println("Error:", err)
		// TODO: Probably wrong Errno here :/
		return nil, 1
	}
	return &iCloudDirStream{*children}, 0
}

// DirStream implementation
type iCloudDirStream struct {
	children []iCloudNode
}

func (stream *iCloudDirStream) HasNext() bool {
	return len(stream.children) != 0
}

func (stream *iCloudDirStream) Next() (fuse.DirEntry, syscall.Errno) {
	next := stream.children[0]
	stream.children = stream.children[1:]
	entry := fuse.DirEntry{
		Name: next.Filename(),
		Ino:  0,
	}
	if next.Extension == nil {
		// TODO: This is a directory, probably a better way to test this?
		entry.Mode = fuse.S_IFDIR
	} else {
		entry.Mode = fuse.S_IFREG
	}
	return entry, 0
}

func (stream *iCloudDirStream) Close() {}

func (node *iCloudNode) stableAttr() fs.StableAttr {
	attr := fs.StableAttr{Ino: node.Hash()}
	if node.Extension == nil {
		// TODO: This is a directory, probably a better way to test this?
		attr.Mode = fuse.S_IFDIR
	}
	return attr
}

func (inode *iCloudInode) generateInode(ctx context.Context, node *iCloudNode) *fs.Inode {
	return inode.NewPersistentInode(
		ctx, &iCloudInode{
			node:  node,
			drive: inode.drive,
		},
		node.stableAttr(),
	)
}

// File Open/Read handling
func (inode *iCloudInode) Open(ctx context.Context, flags uint32) (fh fs.FileHandle, fuseFlags uint32, errno syscall.Errno) {
	bytes, err := inode.drive.GetData(inode.node)
	if err != nil {
		log.Println("Error:", err)
		// TODO: Probably wrong Errno here :/
		return nil, 0, 1
	}
	inode.data = bytes
	inode.dataDirty = false
	return nil, fuse.FOPEN_KEEP_CACHE, 0
}

func (inode *iCloudInode) Read(ctx context.Context, f fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	return inode, 0
}

func (inode *iCloudInode) Size() int {
	return int(len(inode.data))
}

func (inode *iCloudInode) Bytes(buf []byte) ([]byte, fuse.Status) {
	return inode.data, 0
}

func (inode *iCloudInode) Done() {}

func (inode *iCloudInode) Write(ctx context.Context, f fs.FileHandle, data []byte, off int64) (written uint32, errno syscall.Errno) {
	// FIXME: Incorrect offset here it seems, if trying to echo to the end of file we still get 0
	end := int64(len(data)) + off
	if int64(len(inode.data)) < end {
		n := make([]byte, end)
		copy(n, inode.data)
		inode.data = n
	}
	copy(inode.data[off:end], data)
	inode.dataDirty = true
	return uint32(len(data)), 0
}

func (inode *iCloudInode) Flush(ctx context.Context, fh fs.FileHandle) syscall.Errno {
	if !inode.dataDirty {
		// NOOP
		return 0
	}
	err := inode.drive.WriteData(inode.node, inode.data)
	if err != nil {
		log.Printf("Error when flushing: %v", err)
		// TODO: Probably wrong Errno here :/
		return 1
	}
	return 0
}

func main() {
	debug := flag.Bool("debug", false, "print debug data")
	flag.Parse()
	if len(flag.Args()) < 1 {
		log.Fatal("Usage:\n  hello MOUNTPOINT")
	}
	opts := &fs.Options{}
	opts.Debug = *debug

	client := http.Client{}
	client.Jar = AuthenticatedJar()
	drive := iCloudDrive{
		client: client,
	}
	root, err := drive.GetRootNode()
	if err != nil {
		log.Fatalf("Connecting to drive failed: %v\n", err)
	}
	inode := iCloudInode{
		node:  root,
		drive: drive,
	}

	server, err := fs.Mount(flag.Arg(0), &inode, opts)
	if err != nil {
		log.Fatalf("Mount fail: %v\n", err)
	}
	server.Wait()
}
