package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"sync"
)

type CookieJar struct {
	sync.Mutex

	cookies []*http.Cookie
}

func NewCookieJar() *CookieJar {
	jar := new(CookieJar)
	return jar
}

func (j *CookieJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	j.Lock()
	defer j.Unlock()
	// j.cookies = cookies
}

func (j *CookieJar) Cookies(u *url.URL) []*http.Cookie {
	return j.cookies
}

func AuthenticatedJar() *CookieJar {
	jar := NewCookieJar()
	return jar
}

type iCloudDrive struct {
	client http.Client
}

func (drive *iCloudDrive) GetRootNode() *iCloudNode {
	return drive.getNodeData("FOLDER::com.apple.CloudDocs::root")
}

func (drive *iCloudDrive) GetNodeData(item *iCloudNode) *iCloudNode {
	//log.Println("Fetching data for", item)
	//log.Println("drivewsid", item.drivewsid)
	// TODO: Check if we already have all data?
	return drive.getNodeData(item.drivewsid)
}

func (drive *iCloudDrive) getNodeData(drivewsid string) *iCloudNode {
	payload := []GetNodeDataRequest{
		GetNodeDataRequest{
			Drivewsid: drivewsid,
		},
	}
	buf := new(bytes.Buffer)
	json.NewEncoder(buf).Encode(payload)
	req, err := http.NewRequest("POST", "https://p63-drivews.icloud.com/retrieveItemDetailsInFolders", buf)
	if err != nil {
		log.Fatalf("Request creation fail: %v\n", err)
	}
	req.Header.Add("Origin", "https://www.icloud.com")
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("Accept", "application/json")
	resp, err := drive.client.Do(req)
	if err != nil {
		log.Fatalf("Error: %v\n", err)
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Fatalf("Error: %v\n", err)
	}
	//log.Println("body:", string(body))
	response := new([]GetNodeDataResponse)
	json.Unmarshal(body, &response)
	//log.Println("response:", response)
	node := (*response)[0]
	var children []iCloudNode
	for _, item := range node.Items {
		children = append(children, iCloudNode{
			drivewsid: item.Drivewsid,
			docwsid:   item.Docwsid,
			zone:      item.Zone,
			Name:      item.Name,
			Size:      item.Size,
			Extension: item.Extension,
			Etag:      item.Etag,
		})
	}
	return &iCloudNode{
		drivewsid: node.Drivewsid,
		docwsid:   node.Docwsid,
		zone:      node.Zone,
		Name:      node.Name,
		Size:      node.Size,
		Extension: node.Extension,
		Etag:      node.Etag,
		children:  &children,
	}
}

func (drive *iCloudDrive) GetChildren(node *iCloudNode) []iCloudNode {
	if node.children != nil {
		return *node.children
	}
	data := drive.GetNodeData(node)
	node.children = data.children
	return *node.children
}

func (drive *iCloudDrive) GetData(node *iCloudNode) []byte {
	req, err := http.NewRequest(
		"GET",
		fmt.Sprintf("https://p63-docws.icloud.com/ws/%s/download/by_id?document_id=%s", node.zone, node.docwsid),
		nil,
	)
	if err != nil {
		log.Fatalf("Request creation fail: %v\n", err)
	}
	req.Header.Add("Origin", "https://www.icloud.com")
	resp, err := drive.client.Do(req)
	if err != nil {
		log.Fatalf("Error: %v\n", err)
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Fatalf("Error: %v\n", err)
	}
	//log.Println("body:", string(body))
	response := new(DownloadInfo)
	json.Unmarshal(body, &response)

	req, err = http.NewRequest("GET", response.DataToken.Url, nil)
	if err != nil {
		log.Fatalf("Request creation fail: %v\n", err)
	}
	req.Header.Add("Origin", "https://www.icloud.com")
	resp, err = drive.client.Do(req)
	if err != nil {
		log.Fatalf("Error: %v\n", err)
	}
	body, err = ioutil.ReadAll(resp.Body)
	return body
}

type GetNodeDataRequest struct {
	Drivewsid string `json:"drivewsid"`
}

type GetNodeDataResponse struct {
	Drivewsid string         `json:"drivewsid"`
	Docwsid   string         `json:"docwsid"`
	Zone      string         `json:"zone"`
	Name      string         `json:"name"`
	Size      uint64         `json:"size"`
	Type      string         `json:"type"`
	Extension *string        `json:"extension"`
	Etag      string         `json:"etag"`
	Items     []NodeDataItem `json:"items"`
}

type NodeDataItem struct {
	Drivewsid string  `json:"drivewsid"`
	Docwsid   string  `json:"docwsid"`
	Zone      string  `json:"zone"`
	Name      string  `json:"name"`
	Size      uint64  `json:"size"`
	Type      string  `json:"type"`
	Extension *string `json:"extension"`
	Etag      string  `json:"etag"`
}

type DataToken struct {
	Url string `json:"url"`
}

type DownloadInfo struct {
	DataToken DataToken `json:"data_token"`
}

type iCloudNode struct {
	drivewsid string
	zone      string
	docwsid   string
	Name      string
	Size      uint64
	Extension *string
	Etag      string
	children  *[]iCloudNode
}

func (node *iCloudNode) Hash() uint64 {
	h := fnv.New64a()
	h.Write([]byte(node.drivewsid))
	return h.Sum64()
}

func (node *iCloudNode) Filename() string {
	if node.Extension != nil {
		return fmt.Sprintf("%s.%s", node.Name, *node.Extension)
	} else {
		return node.Name
	}
}

type SignInRequest struct {
	Name     string `json:"accountName"`
	Password string `json:"password"`
}

type SignInResponse struct {
	AuthType string `json:"authType"`
}
