package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/cheif/docker-volume-icloud/icloud"
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
	cancelFunc  func()
}

type iCloudDriver struct {
	sync.RWMutex

	root      string
	statePath string
	drive     *icloud.Drive
	volumes   map[string]*iCloudVolume
}

func newIcloudDriver(statePath string) (*iCloudDriver, error) {
	sessionPath := filepath.Join(statePath, "session.json")
	drive, err := icloud.RestoreSession(sessionPath)
	if err != nil {
		// This usually just means there's no session to restore, and this is handled below
	}

	d := &iCloudDriver{
		root:      "/mnt/volumes",
		statePath: filepath.Join(statePath, "state.json"),
		drive:     drive,
		volumes:   map[string]*iCloudVolume{},
	}

	if err := d.restoreState(); err != nil {
		if os.IsNotExist(err) {
			log.Printf("No state to restore")
		} else {
			return nil, err
		}
	}

	if d.drive == nil {
		// No drive has been initialized, start a telnet-session to be able to do this async
		go d.initiateInteractiveSession(sessionPath)
	}

	return d, nil
}

func (d *iCloudDriver) initiateInteractiveSession(sessionPath string) {
	drive, err := icloud.CreateNewSessionInteractive(":5000", sessionPath)
	if err != nil {
		panic("Handle this better")
	}
	d.drive = drive
}

func (d *iCloudDriver) restoreState() error {
	data, err := os.ReadFile(d.statePath)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &d.volumes)
}

func (d *iCloudDriver) saveState() {
	data, err := json.Marshal(d.volumes)
	if err != nil {
		log.Printf("Error marshalling state: %v", err)
		return
	}
	if err := os.WriteFile(d.statePath, data, 0644); err != nil {
		log.Printf("Error saving state: %v", err)
	}
}

func (d *iCloudDriver) checkIfHasSession() error {
	if d.drive == nil {
		return fmt.Errorf("Session not configured. Telnet to :5000 to configure it")
	}
	return nil
}

func (d *iCloudDriver) Capabilities() *volume.CapabilitiesResponse {
	return &volume.CapabilitiesResponse{Capabilities: volume.Capability{Scope: "local"}}
}

// volume.Driver implementation
func (d *iCloudDriver) Create(r *volume.CreateRequest) error {
	err := d.checkIfHasSession()
	if err != nil {
		return err
	}
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

	d.saveState()

	return nil
}

func (d *iCloudDriver) Remove(r *volume.RemoveRequest) error {
	err := d.checkIfHasSession()
	if err != nil {
		return err
	}
	d.Lock()
	defer d.Unlock()

	v, ok := d.volumes[r.Name]
	if !ok {
		return logError("volume %s not found", r.Name)
	}
	if v.connections != 0 {
		return logError("volume %s is currently used by a container", r.Name)
	}
	if err := os.RemoveAll(v.Mountpoint); err != nil {
		return logError(err.Error())
	}
	delete(d.volumes, r.Name)

	d.saveState()

	return nil
}

func (d *iCloudDriver) Get(r *volume.GetRequest) (*volume.GetResponse, error) {
	err := d.checkIfHasSession()
	if err != nil {
		return nil, err
	}
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
	err := d.checkIfHasSession()
	if err != nil {
		return nil, err
	}
	d.Lock()
	defer d.Unlock()

	var vols []*volume.Volume
	for name, v := range d.volumes {
		vols = append(vols, &volume.Volume{Name: name, Mountpoint: v.Mountpoint})
	}
	return &volume.ListResponse{Volumes: vols}, nil
}

func (d *iCloudDriver) Mount(r *volume.MountRequest) (*volume.MountResponse, error) {
	err := d.checkIfHasSession()
	if err != nil {
		return nil, err
	}
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
			MountOptions: fuse.MountOptions{
				Debug: os.Getenv("DEBUG") != "",
			},
		}
		server, err := fs.Mount(v.Mountpoint, &inode, opts)
		if err != nil {
			return nil, logError("Mounting failed: %v", err)
		}
		log.Printf("Serving: %v\n", server)
		v.server = server
		ctx, cancelFunc := context.WithCancel(context.Background())
		v.cancelFunc = cancelFunc
		go func(ctx context.Context) {
			ticker := time.NewTicker(5 * time.Second)
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					inode.ResetFileSystemCacheIfStale()
				}
			}
		}(ctx)
	}

	v.connections++
	return &volume.MountResponse{Mountpoint: v.Mountpoint}, nil
}

func (d *iCloudDriver) Unmount(r *volume.UnmountRequest) error {
	log.Println("Unmount", r)
	err := d.checkIfHasSession()
	if err != nil {
		return err
	}
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
		v.cancelFunc()
		v.connections = 0
	}
	return nil
}

func (d *iCloudDriver) Path(r *volume.PathRequest) (*volume.PathResponse, error) {
	log.Println("Path", r)
	err := d.checkIfHasSession()
	if err != nil {
		return nil, err
	}
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
	createSession := flag.Bool("create-session", false, "Create a new session, passed to stdout")
	username := flag.String("username", "", "Username for creating session")
	password := flag.String("password", "", "Password for creating session")

	flag.Parse()

	if *createSession {
		session, err := icloud.NewSessionData(*username, *password)
		if err != nil {
			log.Fatal(err)
		}
		bytes, err := json.Marshal(session)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%v", string(bytes))
		return
	} else {
		log.SetFlags(log.Lshortfile)
		log.Println("Starting up..")
		d, err := newIcloudDriver("/mnt/state")
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
}
