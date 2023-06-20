package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io/ioutil"
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

func (drive *iCloudDrive) GetRootNode() (*iCloudNode, error) {
	return drive.getNodeData("FOLDER::com.apple.CloudDocs::root")
}

func (drive *iCloudDrive) GetNodeData(node *iCloudNode) (*iCloudNode, error) {
	// This is a proxy for if this node already has all data, or if we need to fetch it to get children etc.
	if !node.shallow {
		return node, nil
	}
	data, err := drive.getNodeData(node.drivewsid)
	if err != nil {
		return nil, err
	}
	node.children = data.children
	node.shallow = false
	return node, nil
}

func (drive *iCloudDrive) getNodeData(drivewsid string) (*iCloudNode, error) {
	payload := []GetNodeDataRequest{
		GetNodeDataRequest{
			Drivewsid: drivewsid,
		},
	}
	buf := new(bytes.Buffer)
	json.NewEncoder(buf).Encode(payload)
	req, err := http.NewRequest("POST", "https://p63-drivews.icloud.com/retrieveItemDetailsInFolders", buf)
	if err != nil {
		return nil, err
	}
	req.Header.Add("Origin", "https://www.icloud.com")
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("Accept", "application/json")
	resp, err := drive.client.Do(req)
	if err != nil {
		return nil, err
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	response := new([]GetNodeDataResponse)
	json.Unmarshal(body, &response)
	node := (*response)[0]
	var children []iCloudNode
	for _, item := range node.Items {
		children = append(children, iCloudNode{
			drivewsid: item.Drivewsid,
			docwsid:   item.Docwsid,
			zone:      item.Zone,
			shallow:   true,
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
		shallow:   false,
		Name:      node.Name,
		Size:      node.Size,
		Extension: node.Extension,
		Etag:      node.Etag,
		children:  &children,
	}, nil
}

func (drive *iCloudDrive) GetChildren(node *iCloudNode) (*[]iCloudNode, error) {
	node, err := drive.GetNodeData(node)
	if err != nil {
		return nil, err
	}
	return node.children, nil
}

func (drive *iCloudDrive) GetData(node *iCloudNode) ([]byte, error) {
	req, err := http.NewRequest(
		"GET",
		fmt.Sprintf("https://p63-docws.icloud.com/ws/%s/download/by_id?document_id=%s", node.zone, node.docwsid),
		nil,
	)
	if err != nil {
		return nil, err
	}
	req.Header.Add("Origin", "https://www.icloud.com")
	resp, err := drive.client.Do(req)
	if err != nil {
		return nil, err
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	response := new(DownloadInfo)
	json.Unmarshal(body, &response)

	req, err = http.NewRequest("GET", response.DataToken.Url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Add("Origin", "https://www.icloud.com")
	resp, err = drive.client.Do(req)
	if err != nil {
		return nil, err
	}
	return ioutil.ReadAll(resp.Body)
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
	shallow   bool
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
