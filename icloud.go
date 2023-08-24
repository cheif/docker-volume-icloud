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
	"strings"
	"sync"
	"time"
)

type CookieJar struct {
	sync.Mutex

	cookies map[string]*http.Cookie
}

func NewCookieJar() *CookieJar {
	jar := new(CookieJar)
	jar.cookies = make(map[string]*http.Cookie)
	return jar
}

func (j *CookieJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	j.Lock()
	defer j.Unlock()
	for _, cookie := range cookies {
		j.cookies[cookie.Name] = cookie
	}
}

func (j *CookieJar) Cookies(u *url.URL) []*http.Cookie {
	cookies := make([]*http.Cookie, 0)
	for _, cookie := range j.cookies {
		cookies = append(cookies, cookie)
	}
	return cookies
}

func AuthenticatedJar(accessToken string, webauthUser string) *CookieJar {
	jar := NewCookieJar()
	jar.SetCookies(nil, []*http.Cookie{
		&http.Cookie{
			Name:  "X-APPLE-WEBAUTH-TOKEN",
			Value: accessToken,
		},
		&http.Cookie{
			Name:  "X-APPLE-WEBAUTH-USER",
			Value: webauthUser,
		},
	})
	return jar
}

type iCloudDrive struct {
	client http.Client
}

func (drive *iCloudDrive) ValidateToken() error {
	req, err := http.NewRequest("POST", "https://setup.icloud.com/setup/ws/1/validate", nil)
	if err != nil {
		return err
	}
	req.Header.Add("Origin", "https://www.icloud.com")
	req.Header.Add("Accept", "application/json")
	resp, err := drive.client.Do(req)
	if err != nil {
		return err
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	response := new(TokenResponse)
	json.Unmarshal(body, &response)
	if response == nil {
		return fmt.Errorf("Unable to validate token")
	}
	log.Println("Validated token for:", response.DsInfo.PrimaryEmail)
	return nil
}

type TokenResponse struct {
	DsInfo DsInfo `json:"dsInfo"`
}

type DsInfo struct {
	PrimaryEmail string `json:"primaryEmail"`
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
	node.setChildren(data.children)
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
	err = json.Unmarshal(body, &response)
	if err != nil {
		return nil, err
	}
	if len(*response) == 0 {
		return nil, fmt.Errorf("Error when parsing getNodeData response: %v", string(body))
	}
	node := (*response)[0]

	parent := &iCloudNode{
		drivewsid: node.Drivewsid,
		docwsid:   node.Docwsid,
		zone:      node.Zone,
		shallow:   false,
		Name:      node.Name,
		Size:      node.Size,
		Extension: node.Extension,
		Etag:      node.Etag,
	}
	var children []iCloudNode
	for _, item := range node.Items {
		children = append(children, iCloudNode{
			drivewsid:   item.Drivewsid,
			docwsid:     item.Docwsid,
			zone:        item.Zone,
			shallow:     item.Type == "FOLDER",
			Name:        item.Name,
			Size:        item.Size,
			Extension:   item.Extension,
			Etag:        item.Etag,
			DateCreated: item.DateCreated,
			DateChanged: item.DateChanged,
		})
	}
	parent.setChildren(&children)
	return parent, nil
}

func (node *iCloudNode) setChildren(children *[]iCloudNode) {
	if children == nil {
		return
	}
	for idx, _ := range *children {
		(*children)[idx].parent = node
	}
	node.children = children
	node.shallow = false
}

func (drive *iCloudDrive) GetChildren(node *iCloudNode) (*[]iCloudNode, error) {
	node, err := drive.GetNodeData(node)
	if err != nil {
		return nil, err
	}
	return node.children, nil
}

func (drive *iCloudDrive) GetNode(path string) (*iCloudNode, error) {
	node, err := drive.GetRootNode()
	if err != nil {
		return nil, err
	}
	for _, component := range strings.Split(path, "/") {
		if component == "" {
			continue
		}
		var child *iCloudNode
		for _, candidate := range *node.children {
			if candidate.Filename() == component {
				child = &candidate
				break
			}
		}
		if child == nil {
			return nil, fmt.Errorf("Could not find component: %s", component)
		}
		node, err = drive.GetNodeData(child)
		if err != nil {
			return nil, err
		}
	}
	return node, nil
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
	err = json.Unmarshal(body, &response)
	if err != nil {
		return nil, err
	}

	req, err = http.NewRequest("GET", response.DataToken.Url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Add("Origin", "https://www.icloud.com")
	resp, err = drive.client.Do(req)
	if err != nil {
		return nil, err
	}
	// TODO: We probably need more strict testing here, but it seems like iCloud responds with a 400 when the file doesn't exist, so we just check for that now
	if resp.StatusCode == http.StatusBadRequest {
		return []byte{}, nil
	}
	return ioutil.ReadAll(resp.Body)
}

func (drive *iCloudDrive) WriteData(node *iCloudNode, data []byte) error {
	uploadData, err := drive.uploadFileData(node)
	if err != nil {
		return err
	}
	buf := bytes.NewBuffer(data)
	req, err := http.NewRequest("POST", uploadData.Url, buf)
	resp, err := drive.client.Do(req)
	if err != nil {
		return err
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	response := new(UploadFileResponse)
	err = json.Unmarshal(body, &response)
	if err != nil {
		return err
	}
	err = drive.updateDocumentLink(node, response.SingleFile)
	if err != nil {
		return err
	}
	// Make sure that the parent re-fetches data for it's children after this write
	node.parent.shallow = true
	return nil
}

func (drive *iCloudDrive) uploadFileData(node *iCloudNode) (*UploadURLResponse, error) {
	payload := UploadURLRequest{
		Filename:    node.Filename(),
		Type:        "FILE",
		ContentType: "",
	}
	buf := new(bytes.Buffer)
	json.NewEncoder(buf).Encode(payload)
	req, err := http.NewRequest(
		"POST",
		fmt.Sprintf("https://p63-docws.icloud.com/ws/%s/upload/web", node.zone),
		buf,
	)
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
	response := new([]UploadURLResponse)
	err = json.Unmarshal(body, &response)
	if err != nil {
		return nil, err
	}
	return &(*response)[0], nil
}

func (drive *iCloudDrive) updateDocumentLink(node *iCloudNode, fileData UploadFileData) error {
	payload := UpdateDocumentLinkRequest{
		DocumentId: node.docwsid,
		Command:    "modify_file",
		Data: UpdateDocumentData{
			ReferenceSignature: fileData.ReferenceChecksum,
			Signature:          fileData.FileChecksum,
			WrappingKey:        fileData.WrappingKey,
			Size:               fileData.Size,
			Receipt:            fileData.Receipt,
		},
		/*
			Path: UpdateDocumentPath{
				StartingDocumentId: "",
				Path:               node.Filename(),
			},
		*/
	}
	buf := new(bytes.Buffer)
	json.NewEncoder(buf).Encode(payload)
	req, err := http.NewRequest(
		"POST",
		fmt.Sprintf("https://p63-docws.icloud.com/ws/%s/update/documents", node.zone),
		buf,
	)
	if err != nil {
		return err
	}
	req.Header.Add("Origin", "https://www.icloud.com")
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("Accept", "application/json")
	_, err = drive.client.Do(req)
	return err
}

type UploadURLRequest struct {
	Filename    string `json:"filename"`
	Type        string `json:"type"`
	ContentType string `json:"content_type"`
}

type UploadURLResponse struct {
	DocumentId string `json:"document_id"`
	Url        string `json:"url"`
}

type UploadFileResponse struct {
	SingleFile UploadFileData `json:"singleFile"`
}

type UploadFileData struct {
	ReferenceChecksum string `json:"referenceChecksum"`
	FileChecksum      string `json:"fileChecksum"`
	WrappingKey       string `json:"wrappingKey"`
	Size              int    `json:"size"`
	Receipt           string `json:"receipt"`
}

type UpdateDocumentLinkRequest struct {
	DocumentId string             `json:"document_id"`
	Command    string             `json:"command"`
	Data       UpdateDocumentData `json:"data"`
}

type UpdateDocumentData struct {
	ReferenceSignature string `json:"reference_signature"`
	Signature          string `json:"signature"`
	WrappingKey        string `json:"wrapping_key"`
	Size               int    `json:"size"`
	Receipt            string `json:"receipt"`
}

type UpdateDocumentPath struct {
	StartingDocumentId string `json:"starting_document_id"`
	Path               string `json:"path"`
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

	DateCreated time.Time `json:"dateCreated"`
	DateChanged time.Time `json:"dateChanged"`
}

type DataToken struct {
	Url string `json:"url"`
}

type DownloadInfo struct {
	DataToken DataToken `json:"data_token"`
}

type iCloudNode struct {
	drivewsid   string
	zone        string
	docwsid     string
	shallow     bool
	Name        string
	Size        uint64
	Extension   *string
	Etag        string
	DateCreated time.Time
	DateChanged time.Time

	parent   *iCloudNode
	children *[]iCloudNode
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
