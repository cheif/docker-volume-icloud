package icloud

import (
	"log"
	"net/http"
	"testing"
	"time"
)

func TestListenToChanges(t *testing.T) {
	drive, err := RestoreSession("/mnt/state/session.json")
	if err != nil {
		t.Error(err)
	}
	err = drive.ValidateToken()
	if err != nil {
		t.Error(err)
	}

	file, err := drive.GetNode("/test/testfile.txt")
	if err != nil {
		t.Error(err)
	}
	log.Println("Observing /test/testfile.txt, please change it!")
	initialDateChanged := file.DateChanged
	log.Println("Initial DateChanged:", initialDateChanged)
	for true {
		time.Sleep(time.Second)
		file, err = drive.GetNode("/test/testfile.txt")
		if err != nil {
			t.Error(err)
		}
		if initialDateChanged != file.DateChanged {
			break
		} else {
			log.Println("Still", file.DateChanged)
		}
	}
	log.Println("New DateChanged:", file.DateChanged)
}

func TestValidateToken(t *testing.T) {
	client := http.Client{}
	client.Jar = AuthenticatedJar("", "")
	drive := Drive{
		client: client,
	}
	err := drive.ValidateToken()
	if err == nil {
		t.Errorf("ValidateToken didn't error out with empty token/user")
	}
}
