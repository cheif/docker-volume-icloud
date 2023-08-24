package main

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/pmezard/go-difflib/difflib"
)

func TestWrite(t *testing.T) {
	inode, err := createInode()
	if err != nil {
		t.Error(err)
	}
	server, err := fs.Mount("/mnt", inode, nil)
	if err != nil {
		t.Error(err)
	}
	defer server.Unmount()

	before, err := readString("/mnt/testfile.txt")
	if err != nil {
		t.Error(err)
	}

	toAppend := fmt.Sprintf("%s\n", time.Now().Format("2006-01-02T15:04:05"))

	err = appendToFile("/mnt/testfile.txt", toAppend)
	if err != nil {
		t.Error(err)
	}

	after, err := readString("/mnt/testfile.txt")
	if err != nil {
		t.Error(err)
	}

	expected := before + toAppend
	diff := diff(after, expected)
	if len(diff) > 0 {
		t.Errorf(diff)
	}
}

func diff(a, b string) string {
	diff := difflib.UnifiedDiff{
		A:        difflib.SplitLines(a),
		B:        difflib.SplitLines(b),
		FromFile: "Actual",
		ToFile:   "Expected",
		Context:  3,
	}
	text, _ := difflib.GetUnifiedDiffString(diff)
	return text
}

func readString(filename string) (string, error) {
	data, err := ioutil.ReadFile(filename)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func appendToFile(filename, fmt string) error {
	f, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(fmt)
	return err
}

func createInode() (*iCloudInode, error) {
	accessToken := os.Getenv("ACCESS_TOKEN")
	if accessToken == "" {
		return nil, fmt.Errorf("ACCESS_TOKEN required!")
	}
	webauthUser := os.Getenv("WEBAUTH_USER")
	if webauthUser == "" {
		return nil, fmt.Errorf("WEBAUTH_USER required!")
	}
	client := http.Client{}
	client.Jar = AuthenticatedJar(accessToken, webauthUser)
	drive := iCloudDrive{
		client: client,
	}
	drive.ValidateToken()
	node, err := drive.GetNode("/test/")
	if err != nil {
		return nil, fmt.Errorf("Connecting to drive failed: %v\n", err)
	}
	inode := iCloudInode{
		node:  node,
		drive: drive,
	}
	return &inode, nil
}
