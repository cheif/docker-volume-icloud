package icloud

import (
	"log"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestListenToChanges(t *testing.T) {
	accessToken := os.Getenv("ACCESS_TOKEN")
	if accessToken == "" {
		log.Fatalf("ACCESS_TOKEN required!")
	}
	webauthUser := os.Getenv("WEBAUTH_USER")
	if webauthUser == "" {
		log.Fatalf("WEBAUTH_USER required!")
	}
	client := http.Client{}
	client.Jar = AuthenticatedJar(accessToken, webauthUser)
	drive := Drive{
		client: client,
	}
	err := drive.ValidateToken()
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
