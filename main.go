package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/docker/go-plugins-helpers/volume"
	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

const socketAddress = "/run/docker/plugins/icloud.sock"

type iCloudVolume struct {
	Path string

	Mountpoint  string
	connections int
	server      *fuse.Server
}

type iCloudDriver struct {
	sync.RWMutex

	root    string
	drive   *iCloudDrive
	volumes map[string]*iCloudVolume
}

func newIcloudDriver(accessToken string, webauthUser string) (*iCloudDriver, error) {

	client := http.Client{}
	client.Jar = AuthenticatedJar(accessToken, webauthUser)
	drive := iCloudDrive{
		client: client,
	}
	err := drive.ValidateToken()
	if err != nil {
		return nil, err
	}

	d := &iCloudDriver{
		root:    "/mnt/volumes",
		drive:   &drive,
		volumes: map[string]*iCloudVolume{},
	}

	return d, nil
}

func (d *iCloudDriver) Capabilities() *volume.CapabilitiesResponse {
	return &volume.CapabilitiesResponse{Capabilities: volume.Capability{Scope: "local"}}
}

// volume.Driver implementation
func (d *iCloudDriver) Create(r *volume.CreateRequest) error {
	log.Println("Creating volume", r.Name)

	d.Lock()
	defer d.Unlock()

	v := &iCloudVolume{Mountpoint: filepath.Join(d.root, r.Name)}

	for key, val := range r.Options {
		switch key {
		case "path":
			v.Path = val
		}
	}

	if v.Path == "" {
		return logError("'path' is required")
	}

	d.volumes[r.Name] = v

	return nil
}

func (d *iCloudDriver) Remove(r *volume.RemoveRequest) error {
	return nil
}

func (d *iCloudDriver) Get(r *volume.GetRequest) (*volume.GetResponse, error) {
	log.Println("Getting", r)

	d.Lock()
	defer d.Unlock()

	v, ok := d.volumes[r.Name]
	if !ok {
		return &volume.GetResponse{}, logError("volume %s not found", r.Name)
	}

	return &volume.GetResponse{Volume: &volume.Volume{Name: r.Name, Mountpoint: v.Mountpoint}}, nil
}

func (d *iCloudDriver) List() (*volume.ListResponse, error) {
	d.Lock()
	defer d.Unlock()

	var vols []*volume.Volume
	for name, v := range d.volumes {
		vols = append(vols, &volume.Volume{Name: name, Mountpoint: v.Mountpoint})
	}
	return &volume.ListResponse{Volumes: vols}, nil
}

func (d *iCloudDriver) Mount(r *volume.MountRequest) (*volume.MountResponse, error) {
	log.Println("Mount", r)
	d.Lock()
	defer d.Unlock()

	v, ok := d.volumes[r.Name]
	if !ok {
		return &volume.MountResponse{}, logError("volume %s not found", r.Name)
	}

	if v.connections == 0 {
		fi, err := os.Lstat(v.Mountpoint)
		if os.IsNotExist(err) {
			if err := os.MkdirAll(v.Mountpoint, 0755); err != nil {
				return &volume.MountResponse{}, logError(err.Error())
			}
		} else if err != nil {
			return &volume.MountResponse{}, logError(err.Error())
		}

		if fi != nil && !fi.IsDir() {
			return &volume.MountResponse{}, logError("%v already exist and it's not a directory", v.Mountpoint)
		}

		node, err := d.drive.GetNode(v.Path)
		if err != nil {
			return nil, logError("Connecting to drive failed: %v\n", err)
		}
		inode := iCloudInode{
			node:  node,
			drive: *d.drive,
		}

		timeout := time.Second * 10
		opts := &fs.Options{
			EntryTimeout: &timeout,
			AttrTimeout:  &timeout,
		}
		server, err := fs.Mount(v.Mountpoint, &inode, opts)
		if err != nil {
			return nil, logError("Mounting failed: %v", err)
		}
		log.Printf("Serving: %v\n", server)
		v.server = server
	}

	v.connections++
	return &volume.MountResponse{Mountpoint: v.Mountpoint}, nil
}

func (d *iCloudDriver) Unmount(r *volume.UnmountRequest) error {
	log.Println("Unmount", r)
	d.Lock()
	defer d.Unlock()

	v, ok := d.volumes[r.Name]
	if !ok {
		return logError("volume %s not found", r.Name)
	}

	v.connections--

	if v.connections <= 0 {
		if err := v.server.Unmount(); err != nil {
			return logError(err.Error())
		}
		v.connections = 0
	}
	return nil
}

func (d *iCloudDriver) Path(r *volume.PathRequest) (*volume.PathResponse, error) {
	log.Println("Path", r)
	d.RLock()
	defer d.RUnlock()

	v, ok := d.volumes[r.Name]
	if !ok {
		return &volume.PathResponse{}, logError("volume %s not found", r.Name)
	}

	return &volume.PathResponse{Mountpoint: v.Mountpoint}, nil
}

func logError(format string, args ...interface{}) error {
	err := fmt.Errorf(format, args...)
	log.Println(err)
	return err
}

func main() {
	log.SetFlags(log.Lshortfile)
	log.Println("Starting up..")
	accessToken := os.Getenv("ACCESS_TOKEN")
	if accessToken == "" {
		log.Fatalf("ACCESS_TOKEN required!")
	}
	webauthUser := os.Getenv("WEBAUTH_USER")
	if webauthUser == "" {
		log.Fatalf("WEBAUTH_USER required!")
	}
	d, err := newIcloudDriver(accessToken, webauthUser)
	if err != nil {
		log.Fatal(err)
	}
	h := volume.NewHandler(d)
	fmt.Println("Handler gotten")

	fmt.Println("Serving at", socketAddress)

	err = h.ServeUnix(socketAddress, 0)
	if err != nil {
		log.Fatal(err)
	}
	log.Println("Serving socket")
}
