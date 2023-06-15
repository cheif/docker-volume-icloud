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

	node  *iCloudNode
	drive iCloudDrive
}

func (inode *iCloudInode) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	log.Println("Getattr", inode.node)
	out.Attr.Size = inode.node.Size
	return 0
}

var _ = (fs.NodeReaddirer)((*iCloudInode)(nil))

func (inode *iCloudInode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	log.Println("Lookup", inode.node)
	for _, node := range inode.drive.GetChildren(inode.node) {
		if node.Filename() == name {
			return inode.generateInode(ctx, &node), 0
		}
	}
	return nil, 0
}

func (inode *iCloudInode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	children := inode.drive.GetChildren(inode.node)
	return &iCloudDirStream{children}, 0
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
	return nil, fuse.FOPEN_KEEP_CACHE, 0
}

func (inode *iCloudInode) Read(ctx context.Context, f fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	return inode, 0
}

func (inode *iCloudInode) Size() int {
	return int(inode.node.Size)
}

func (inode *iCloudInode) Bytes(buf []byte) ([]byte, fuse.Status) {
	return inode.drive.GetData(inode.node), 0
}

func (r *iCloudInode) Done() {}

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
	root := drive.GetRootNode()
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
