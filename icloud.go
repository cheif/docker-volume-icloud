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
	for _, cookie := range cookies {
		j.cookies = append(j.cookies, cookie)
	}
}

func (j *CookieJar) Cookies(u *url.URL) []*http.Cookie {
	return j.cookies
}

func AuthenticatedJar(accessToken string, webauthUser string) *CookieJar {
	jar := NewCookieJar()
	jar.cookies = []*http.Cookie{
		&http.Cookie{
			Name:  "X-APPLE-WEBAUTH-TOKEN",
			Value: accessToken,
		},
		&http.Cookie{
			Name:  "X-APPLE-WEBAUTH-USER",
			Value: webauthUser,
		},
	}
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
	if len(*response) == 0 {
		return nil, fmt.Errorf("Error when parsing getNodeData response: %v", string(body))
	}
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
	return drive.updateDocumentLink(node, response.SingleFile)
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
