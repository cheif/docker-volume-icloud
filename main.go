package main

import (
	"net/http"
	"path/filepath"
)


func main() {
	statePath := "/mnt/state"
	sessionPath := filepath.Join(statePath, "session.json")
	handler := NewICloudFileSystemHandler(sessionPath)
	http.ListenAndServe(":8080", &handler)
}
