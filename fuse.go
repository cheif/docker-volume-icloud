package main

import (
	"context"
	"log"
	"syscall"

	"github.com/cheif/docker-volume-icloud/icloud"
	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

type iCloudInode struct {
	fs.Inode

	node  *icloud.Node
	drive icloud.Drive
}

// Node types must be InodeEmbedders
var _ = (fs.InodeEmbedder)((*iCloudInode)(nil))

var _ = (fs.NodeLookuper)((*iCloudInode)(nil))
var _ = (fs.NodeSetattrer)((*iCloudInode)(nil))
var _ = (fs.NodeGetattrer)((*iCloudInode)(nil))

func (inode *iCloudInode) ResetFileSystemCacheIfStale() {
	if len(inode.Children()) > 0 {
		// We've got cached data, check if it has changed
		hasChanges, _ := inode.drive.CheckIfHasNewChanges()
		if hasChanges {
			inode.node, _ = inode.drive.RefreshNodeData(inode.node)
			for _, child := range inode.Children() {
				inode.NotifyEntry(child.Path(nil))
			}
		}
	}
}

func (inode *iCloudInode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	children, err := inode.drive.GetChildren(inode.node)
	if err != nil {
		log.Println("Error:", err)
		// TODO: Probably wrong Errno here :/
		return nil, 1
	}
	for _, node := range *children {
		if node.Filename() == name {
			out.Mode = 0644
			out.Size = node.Size
			out.SetTimes(
				nil,
				&node.DateChanged,
				nil,
			)
			return inode.generateInode(ctx, &node), 0
		}
	}
	return nil, syscall.ENOENT
}

func (inode *iCloudInode) Setattr(ctx context.Context, f fs.FileHandle, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	log.Println("No-Op SetAttr", f, in)
	return 0
}

func (inode *iCloudInode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = 0644
	out.Size = inode.node.Size
	out.SetTimes(
		nil,
		&inode.node.DateChanged,
		nil,
	)
	return 0
}

func (inode *iCloudInode) generateInode(ctx context.Context, node *icloud.Node) *fs.Inode {
	newNode := inode.NewInode(
		ctx,
		&iCloudInode{
			node:  node,
			drive: inode.drive,
		},
		stableAttr(node),
	)
	log.Println("New iNnode", newNode, node.DateChanged)
	return newNode
}

func stableAttr(node *icloud.Node) fs.StableAttr {
	attr := fs.StableAttr{}
	if node.Extension == nil {
		// TODO: This is a directory, probably a better way to test this?
		attr.Mode = fuse.S_IFDIR
	}
	return attr
}

var _ = (fs.NodeReaddirer)((*iCloudInode)(nil))

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
	children []icloud.Node
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

// File Open/Read handling
func (inode *iCloudInode) Open(ctx context.Context, flags uint32) (fh fs.FileHandle, fuseFlags uint32, errno syscall.Errno) {
	file := iCloudFile{
		inode: inode,
	}
	return &file, fuse.FOPEN_KEEP_CACHE, 0
}

type iCloudFile struct {
	inode *iCloudInode

	data  *[]byte
	dirty bool
}

var _ = (fs.FileReader)((*iCloudFile)(nil))
var _ = (fs.FileWriter)((*iCloudFile)(nil))
var _ = (fs.FileFlusher)((*iCloudFile)(nil))

func (file *iCloudFile) ensureDataFetched() syscall.Errno {
	if file.data == nil {
		bytes, err := file.inode.drive.GetData(file.inode.node)
		if err != nil {
			log.Println("Error:", err)
			// TODO: Probably wrong Errno here :/
			return 1
		}
		file.data = &bytes
	}
	return 0
}

func (file *iCloudFile) Read(ctx context.Context, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	err := file.ensureDataFetched()
	if err != 0 {
		return nil, err
	}
	bytes := *file.data
	end := int(off) + len(dest)
	if end > len(bytes) {
		end = len(bytes)
	}
	copy(dest, bytes[off:end])
	return fuse.ReadResultData(dest), 0
}

func (file *iCloudFile) Write(ctx context.Context, data []byte, off int64) (written uint32, errno syscall.Errno) {
	err := file.ensureDataFetched()
	if err != 0 {
		return 0, err
	}
	// FIXME: Incorrect offset here it seems, if trying to echo to the end of file we still get 0
	end := int64(len(data)) + off
	if int64(len(*file.data)) < end {
		n := make([]byte, end)
		copy(n, *file.data)
		*file.data = n
	}
	copy((*file.data)[off:end], data)
	file.dirty = true
	return uint32(len(data)), 0
}

func (file *iCloudFile) Flush(ctx context.Context) syscall.Errno {
	if !file.dirty {
		// NOOP
		return 0
	}
	err := file.inode.drive.WriteData(file.inode.node, *file.data)
	if err != nil {
		log.Printf("Error when flushing: %v", err)
		// TODO: Probably wrong Errno here :/
		return 1
	}
	return 0
}

/*
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
*/
