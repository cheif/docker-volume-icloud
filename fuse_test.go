package main

import (
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/cheif/docker-volume-icloud/icloud"
	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"github.com/pmezard/go-difflib/difflib"
)

func TestWrite(t *testing.T) {
	inode, err := createInode()
	if err != nil {
		t.Error(err)
	}
	server, err := fs.Mount("/mnt/volumes", inode, nil)
	if err != nil {
		t.Error(err)
	}
	defer server.Unmount()

	before, err := readString("/mnt/volumes/testfile.txt")
	if err != nil {
		t.Error(err)
	}
	statBefore, err := os.Stat("/mnt/volumes/testfile.txt")
	if err != nil {
		t.Error(err)
	}

	toAppend := fmt.Sprintf("%s\n", time.Now().Format("2006-01-02T15:04:05"))

	err = appendToFile("/mnt/volumes/testfile.txt", toAppend)
	if err != nil {
		t.Error(err)
	}

	after, err := readString("/mnt/volumes/testfile.txt")
	if err != nil {
		t.Error(err)
	}

	expected := before + toAppend
	diff := diff(after, expected)
	if len(diff) > 0 {
		t.Errorf(diff)
	}

	statAfter, err := os.Stat("/mnt/volumes/testfile.txt")
	if err != nil {
		t.Error(err)
	}

	if !statAfter.ModTime().After(statBefore.ModTime()) {
		t.Errorf("ModTime hasn't changed, was: %s, now: %s", statBefore.ModTime(), statBefore.ModTime())
	}

	if int(statAfter.Size()) != len(expected) {
		t.Errorf("Incorrect Size returned by Stat: %v, expected: %v\n", statAfter.Size(), len(expected))
	}
}

func TestTruncate(t *testing.T) {
	inode, err := createInode()
	if err != nil {
		t.Error(err)
	}
	server, err := fs.Mount("/mnt/volumes", inode, nil)
	if err != nil {
		t.Error(err)
	}
	defer server.Unmount()
	filename := "/mnt/volumes/testfile.txt"

	toAppend := fmt.Sprintf("%s\n", time.Now().Format("2006-01-02T15:04:05"))

	err = appendToFile(filename, toAppend)
	if err != nil {
		t.Error(err)
	}

	before, err := readString(filename)
	if err != nil {
		t.Error(err)
	}
	statBefore, err := os.Stat(filename)
	if err != nil {
		t.Error(err)
	}

	err = truncateFile(filename, statBefore.Size()-10)
	if err != nil {
		t.Error(err)
	}

	after, err := readString(filename)
	if err != nil {
		t.Error(err)
	}

	expected := before[:len(before)-10]
	diff := diff(after, expected)
	if len(diff) > 0 {
		t.Errorf(diff)
	}

	statAfter, err := os.Stat(filename)
	if err != nil {
		t.Error(err)
	}

	if !statAfter.ModTime().After(statBefore.ModTime()) {
		t.Errorf("ModTime hasn't changed, was: %s, now: %s", statBefore.ModTime(), statBefore.ModTime())
	}
}

func TestReadTwice(t *testing.T) {
	inode, err := createInode()
	if err != nil {
		t.Error(err)
	}
	server, err := fs.Mount("/mnt/volumes", inode, nil)
	if err != nil {
		t.Error(err)
	}
	defer server.Unmount()

	before, err := readString("/mnt/volumes/testfile.txt")
	if err != nil {
		t.Error(err)
	}

	after, err := readString("/mnt/volumes/testfile.txt")
	if err != nil {
		t.Error(err)
	}

	diff := diff(after, before)
	if len(diff) > 0 {
		t.Errorf(diff)
	}
}

func TestEchoAndRead(t *testing.T) {
	inode, err := createInode()
	if err != nil {
		t.Error(err)
	}
	server, err := fs.Mount("/mnt/volumes", inode, nil)
	if err != nil {
		t.Error(err)
	}
	defer server.Unmount()

	before, err := readString("/mnt/volumes/testfile.txt")
	if err != nil {
		t.Error(err)
	}

	toAppend := fmt.Sprintf("%s\n", time.Now().Format("2006-01-02T15:04:05"))

	err = exec.Command("sh", "-c", fmt.Sprintf(`echo -n "%s" >> /mnt/volumes/testfile.txt`, toAppend)).Run()
	if err != nil {
		t.Error(err)
	}

	after, err := readString("/mnt/volumes/testfile.txt")
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
	data, err := os.ReadFile(filename)
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

func truncateFile(filename string, newLength int64) error {
	f, err := os.OpenFile(filename, os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	return f.Truncate(newLength)
}

func createInode() (*iCloudInode, error) {
	drive, err := icloud.RestoreSession("/mnt/state/session.json")
	if err != nil {
		return nil, fmt.Errorf("Connecting to drive failed: %v\n", err)
	}
	node, err := drive.GetNode("/test/")
	if err != nil {
		return nil, fmt.Errorf("Connecting to drive failed: %v\n", err)
	}
	inode := iCloudInode{
		node:  node,
		drive: *drive,
	}
	return &inode, nil
}

func debugOpts() *fs.Options {
	timeout := time.Second
	return &fs.Options{
		MountOptions: fuse.MountOptions{
			Debug: true,
		},
		EntryTimeout: &timeout,
		AttrTimeout:  &timeout,
	}
}
