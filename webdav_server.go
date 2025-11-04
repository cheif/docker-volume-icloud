package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"slices"
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
			hits: make(map[string]icloud.Node),
			missing: []string{},
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
	hits map[string]icloud.Node
	missing []string
	lastStaleCheck time.Time
}

func (fs *ICloudFileSystem) checkIfCacheIsStale() {
	if fs.nodeCache.lastStaleCheck.Add(time.Second * 5).Before(time.Now()) {
		// Check if we need to reset cache
		hasChanges, _ := fs.drive.CheckIfHasNewChanges()
		if hasChanges {
			// Just wipe cache, no need to be smart
			fs.nodeCache = nodeCache{
				hits: make(map[string]icloud.Node),
			missing: []string{},
			}
		}
		fs.nodeCache.lastStaleCheck = time.Now()
	}
}

func (c nodeCache) getCached(path string) (*icloud.Node, bool) {
	if slices.Contains(c.missing, path) {
		return nil, true
	}
	node := c.hits[path]
	if (node != icloud.Node{}) {
		return &node, false
	}
	return nil, false
}

func (fs *ICloudFileSystem) getCachedNode(path string) (*icloud.Node, error) {
	path = filepath.Clean(path)
	node, missing := fs.nodeCache.getCached(path)
	if missing {
		return nil, fmt.Errorf("No node at: %v", path)
	}
	if node != nil {
		return node, nil
	}
	// We didn't find the node in the cache, but we might be able to find it through it's parent
	parentName := filepath.Dir(path)
	parent, _ := fs.nodeCache.getCached(parentName)
	if parent != nil {
		children, err := fs.drive.GetChildren(parent)
		if err == nil {
			// Cache all children
			for _, child := range *children {
				childPath := filepath.Join(parentName, child.Filename())
				fs.nodeCache.hits[childPath] = child
			}
		}
		node, _ := fs.nodeCache.getCached(path)
		if node != nil {
			return node, nil
		}
	} else {
		// No cached parent, we need to fetch this from icloud
		node, err := fs.drive.GetNode(path)
		if err != nil {
			return nil, err
		}
		fs.nodeCache.hits[path] = *node
		return node, nil
	}
	fs.nodeCache.missing = append(fs.nodeCache.missing, path)
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
	data, err := fs.drive.GetData(node)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (fs *ICloudFileSystem) Stat(ctx context.Context, name string) (*webdav.FileInfo, error) {
	fmt.Println("Stat", name)
	node, err := fs.getCachedNode(name)
	if err != nil {
		return nil, err
	}
	return createFileInfo(name, node), nil
}

func (fs *ICloudFileSystem) ReadDir(ctx context.Context, name string, recursive bool) ([]webdav.FileInfo, error) {
	fmt.Println("ReadDir", name)
	node, err := fs.getCachedNode(name)
	if err != nil {
		return nil, err
	}
	children, err := fs.drive.GetChildren(node)
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
	return nil, false, fmt.Errorf("Not implemented")
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
