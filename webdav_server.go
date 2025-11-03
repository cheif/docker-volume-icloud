package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"

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
	}
	handler := webdav.Handler{FileSystem: filesystem}
	return func(w http.ResponseWriter, r *http.Request) {
		if filesystem.drive == nil {
			go filesystem.initiateInteractiveSession(sessionPath)
			fmt.Fprintln(w, "Telnet to :5000 to setup iCloud session")
		} else {
			// Everything is properly setup
			handler.ServeHTTP(w, r)
		}
	}
}

type ICloudFileSystem struct {
	drive *icloud.Drive
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
	node, err := fs.drive.GetNode(name)
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
	node, err := fs.drive.GetNode(name)
	if err != nil {
		return nil, err
	}
	fmt.Println("Node", node)
	return createFileInfo(name, node), nil
}

func (fs *ICloudFileSystem) ReadDir(ctx context.Context, name string, recursive bool) ([]webdav.FileInfo, error) {
	fmt.Println("ReadDir", name)
	node, err := fs.drive.GetNode(name)
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
	fmt.Println("Fileinfos", fileInfos)
	return fileInfos, nil
}

func createFileInfo(path string, node *icloud.Node) *webdav.FileInfo {
	return &webdav.FileInfo{
		Path:     path,
		Size:     int64(node.Size),
		ModTime:  node.DateChanged,
		IsDir:    node.Extension == nil,
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
