package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"time"

	"github.com/cheif/docker-volume-icloud/icloud"
	"github.com/emersion/go-webdav"
)

func NewICloudFileSystemHandler(sessionPath string) http.HandlerFunc {
	drive, err := icloud.RestoreSession(sessionPath)
	if err != nil {
		// This usually just means there's no session to restore, and this is handled below
	}
	filesystem := &ICloudFileSystem{
		drive: drive,
		nodeCache: nodeCache{
			nodes: make(map[string]*cachedNode),
		},
	}
	handler := webdav.Handler{FileSystem: filesystem}
	return func(w http.ResponseWriter, r *http.Request) {
		if filesystem.drive == nil {
			go filesystem.initiateInteractiveSession(sessionPath)
			fmt.Fprintln(w, "Telnet to :5000 to setup iCloud session")
		} else {
			// Everything is properly setup
			filesystem.checkIfCacheIsStale()
			handler.ServeHTTP(w, r)
		}
	}
}

type ICloudFileSystem struct {
	drive *icloud.Drive
	nodeCache nodeCache
}

type nodeCache struct {
	nodes map[string]*cachedNode
	lastStaleCheck time.Time
}

type cachedNode struct {
	node *icloud.Node
	data  *[]byte
}


func (fs *ICloudFileSystem) checkIfCacheIsStale() {
	if fs.nodeCache.lastStaleCheck.Add(time.Second * 5).Before(time.Now()) {
		// Check if we need to reset cache
		hasChanges, _ := fs.drive.CheckIfHasNewChanges()
		if hasChanges {
			// Just wipe cache, no need to be smart
			fs.nodeCache = nodeCache{
				nodes: make(map[string]*cachedNode),
			}
		}
		fs.nodeCache.lastStaleCheck = time.Now()
	}
}

func (c nodeCache) getCached(path string) *cachedNode {
	return c.nodes[path]
}

func (fs *ICloudFileSystem) getCachedNode(path string) (*cachedNode, error) {
	path = filepath.Clean(path)
	node := fs.nodeCache.getCached(path)
	if node != nil {
		if node.node != nil {
			return node, nil
		} else {
			return nil, fmt.Errorf("No node at: %v", path)
		}
	}
	// We didn't find the node in the cache, but we might be able to find it through it's parent
	parentName := filepath.Dir(path)
	parent := fs.nodeCache.getCached(parentName)
	if parent != nil {
		children, err := fs.drive.GetChildren(parent.node)
		if err == nil {
			// Cache all children
			for _, child := range *children {
				childPath := filepath.Join(parentName, child.Filename())
				node := child
				cached := cachedNode{
					node: &node,
				}
				fs.nodeCache.nodes[childPath] = &cached
			}
		}
		node := fs.nodeCache.getCached(path)
		if node != nil {
			return node, nil
		}
	} else {
		// No cached parent, we need to fetch this from icloud
		node, err := fs.drive.GetNode(path)
		if err != nil {
			return nil, err
		}
		cached := &cachedNode{
			node: node,
		}
		fs.nodeCache.nodes[path] = cached
		return cached, nil
	}
	fs.nodeCache.nodes[path] = &cachedNode{}
	return nil, fmt.Errorf("No node at: %v", path)
}

func (fs *ICloudFileSystem) initiateInteractiveSession(sessionPath string) {
	drive, err := icloud.CreateNewSessionInteractive(":5000", sessionPath)
	if err != nil {
		panic("Handle this better")
	}
	fs.drive = drive
}

func (fs *ICloudFileSystem) Open(ctx context.Context, name string) (io.ReadCloser, error) {
	fmt.Println("Open", name)
	node, err := fs.getCachedNode(name)
	if err != nil {
		return nil, err
	}
	if node.data != nil {
		// Use data that's already cached
		return io.NopCloser(bytes.NewReader(*node.data)), nil
	}
	data, err := fs.drive.GetData(node.node)
	if err != nil {
		return nil, err
	}
	node.data = &data
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (fs *ICloudFileSystem) Stat(ctx context.Context, name string) (*webdav.FileInfo, error) {
	fmt.Println("Stat", name)
	node, err := fs.getCachedNode(name)
	if err != nil {
		return nil, err
	}
	return createFileInfo(name, node.node), nil
}

func (fs *ICloudFileSystem) ReadDir(ctx context.Context, name string, recursive bool) ([]webdav.FileInfo, error) {
	fmt.Println("ReadDir", name)
	node, err := fs.getCachedNode(name)
	if err != nil {
		return nil, err
	}
	children, err := fs.drive.GetChildren(node.node)
	if err != nil {
		return nil, err
	}
	var fileInfos []webdav.FileInfo
	for _, node := range *children {
		path := filepath.Join(name, node.Filename())
		fileInfos = append(fileInfos, *createFileInfo(path, &node))
	}
	return fileInfos, nil
}

func createFileInfo(path string, node *icloud.Node) *webdav.FileInfo {
	return &webdav.FileInfo{
		Path:     path,
		Size:     int64(node.Size),
		ModTime:  node.DateChanged,
		IsDir:    node.Type == icloud.NodeTypeFolder,
		MIMEType: "test",
		ETag:     node.Etag,
	}
}

func (fs *ICloudFileSystem) Create(ctx context.Context, name string, body io.ReadCloser, opts *webdav.CreateOptions) (*webdav.FileInfo, bool, error) {
	fmt.Println("Create", name)
	node, err := fs.getCachedNode(name)
	if err != nil {
		// We only allow updating file contents for now
		return nil, false, err
	}
	err = fs.drive.WriteDataReader(node.node, body)
	if err != nil {
		return nil, false, err
	}
	fs.checkIfCacheIsStale()
	node, err = fs.getCachedNode(name)
	if err != nil {
		return nil, false, err
	}
	return createFileInfo(name, node.node), false, nil
}

func (fs *ICloudFileSystem) RemoveAll(ctx context.Context, name string, opts *webdav.RemoveAllOptions) error {
	fmt.Println("RemoveAll", name)
	return fmt.Errorf("Not implemented")
}

func (fs *ICloudFileSystem) Mkdir(ctx context.Context, name string) error {
	fmt.Println("Mkdir", name)
	return fmt.Errorf("Not implemented")
}

func (fs *ICloudFileSystem) Copy(ctx context.Context, name, dest string, opts *webdav.CopyOptions) (bool, error) {
	fmt.Println("Copy", name)
	return false, fmt.Errorf("Not implemented")
}

func (fs *ICloudFileSystem) Move(ctx context.Context, name, dest string, opts *webdav.MoveOptions) (bool, error) {
	fmt.Println("Move", name)
	return false, fmt.Errorf("Not implemented")
}
