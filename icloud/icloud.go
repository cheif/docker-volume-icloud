package icloud

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
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
		{
			Name:  "X-APPLE-WEBAUTH-TOKEN",
			Value: accessToken,
		},
		{
			Name:  "X-APPLE-WEBAUTH-USER",
			Value: webauthUser,
		},
	})
	return jar
}

type Drive struct {
	client             http.Client
	continuationMarker *string
}

func NewDrive(client http.Client) Drive {
	return Drive{
		client: client,
	}
}

type SessionData struct {
	Username          string `json:"username"`
	Password          string `json:"password"`
	SessionToken      string `json:"sessionToken"`
	AccountCountyCode string `json:"accountCountryCode"`
	Scnt              string `json:"scnt"`
	SessionId         string `json:"sessionId"`
	TwoFactorToken    string `json:"twoFactorToken"`
}

func newSessionData(headers http.Header) (*SessionData, error) {
	sessionToken := headers.Get("X-Apple-Session-Token")
	accountCountryCode := headers.Get("X-Apple-Id-Account-Country")
	if sessionToken == "" || accountCountryCode == "" {
		return nil, fmt.Errorf("Could not find required headers in %v", headers)
	}
	sessionData := &SessionData{
		SessionToken:      sessionToken,
		AccountCountyCode: accountCountryCode,
		Scnt:              headers.Get("Scnt"),
		SessionId:         headers.Get("X-Apple-ID-Session-Id"),
		TwoFactorToken:    headers.Get("X-Apple-Twosv-Trust-Token"),
	}
	return sessionData, nil
}

func RestoreSession(path string) (*Drive, error) {
	dat, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var sessionData SessionData
	err = json.Unmarshal(dat, &sessionData)
	if err != nil {
		return nil, err
	}

	log.Println("sessionData", sessionData)
	drive, newSession, err := newDriveForSession(sessionData)
	if err != nil {
		return nil, err
	}
	dat, err = json.Marshal(newSession)
	if err == nil {
		os.WriteFile(path, dat, 0644)
	}
	return drive, nil
}

func newDriveForSession(sessionData SessionData) (*Drive, *SessionData, error) {
	client := http.Client{}
	client.Jar = NewCookieJar()
	drive := NewDrive(client)
	requires2FA, err := drive.authenticate(sessionData)
	if err != nil {
		// Getting an error here probably means that the session-token is old, try to login using the sessionData, to get a new session instead
		newSessionData, err := drive.loginUsingSession(sessionData)
		if err == nil {
			return newDriveForSession(*newSessionData)
		}
		return nil, nil, err
	}
	if requires2FA {
		return nil, nil, fmt.Errorf("Session requires 2fa, create a new instead")
	}
	return &drive, &sessionData, nil
}

func CreateNewSessionInteractive(port string, storagePath string) (*Drive, error) {
	drive, newSessionData, err := createNewSessionInteractive(port)
	if err != nil {
		return nil, err
	}

	dat, err := json.Marshal(newSessionData)
	if err == nil {
		os.WriteFile(storagePath, dat, 0644)
	}
	return drive, nil
}

func createNewSessionInteractive(port string) (*Drive, *SessionData, error) {
	log.Println("Creating interactive session over telnet")
	sock, _ := net.Listen("tcp", port)
	conn, err := sock.Accept()
	defer conn.Close()
	if err != nil {
		return nil, nil, err
	}
	username, err := getString(conn, "username/email:")
	if err != nil {
		return nil, nil, err
	}
	password, err := getString(conn, "password:")
	if err != nil {
		return nil, nil, err
	}

	client := http.Client{}
	client.Jar = NewCookieJar()
	drive := NewDrive(client)
	sessionData, err := drive.login(username, password, []string{})
	if err != nil {
		return nil, nil, err
	}
	requires2FA, err := drive.authenticate(*sessionData)
	if err != nil {
		return nil, nil, err
	}
	if requires2FA {
		verificationCode, err := getString(conn, "2FA verification code:")
		if err != nil {
			return nil, nil, err
		}
		err = drive.validate2FA(verificationCode, sessionData)
		if err != nil {
			return nil, nil, err
		}
		newSessionData, err := drive.trustSession(sessionData)
		if err != nil {
			return nil, nil, err
		}
		return newDriveForSession(*newSessionData)
	} else {
		return &drive, sessionData, nil
	}
}

func getString(conn net.Conn, prompt string) (string, error) {
	_, err := fmt.Fprintln(conn, prompt)
	if err != nil {
		return "", err
	}
	buf := make([]byte, 256)
	len, err := conn.Read(buf)
	if err != nil {
		return "", err
	}
	res := string(buf[:len])

	return strings.TrimSuffix(res, "\r\n"), nil
}

func (drive *Drive) loginUsingSession(sessionData SessionData) (*SessionData, error) {
	newSession, err := drive.login(sessionData.Username, sessionData.Password, []string{sessionData.TwoFactorToken})
	if err != nil {
		return nil, err
	}
	// Copy some properties from the old session, that's probably not generated again
	if newSession.TwoFactorToken == "" {
		newSession.TwoFactorToken = sessionData.TwoFactorToken
	}
	newSession.Username = sessionData.Username
	newSession.Password = sessionData.Password
	return newSession, err
}

func (drive *Drive) login(username string, password string, trustTokens []string) (*SessionData, error) {
	payload := LoginRequest{
		AccountName: username,
		Password:    password,
		RememberMe:  true,
		TrustTokens: trustTokens,
	}
	buf := new(bytes.Buffer)
	json.NewEncoder(buf).Encode(payload)
	req, err := http.NewRequest("POST", "https://idmsa.apple.com/appleauth/auth/signin", buf)
	if err != nil {
		return nil, err
	}
	req.Header.Add("Origin", "https://www.icloud.com")
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("Accept", "application/json")

	req.Header.Add("X-Apple-Widget-Key", "d39ba9916b7251055b22c7f910e2ea796ee65e98b2ddecea8f5dde8d9d1a815d")

	resp, err := drive.client.Do(req)
	if err != nil {
		return nil, err
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	response := new(LoginResponse)
	err = json.Unmarshal(body, &response)
	if err != nil {
		return nil, err
	}
	return newSessionData(resp.Header)
}

type LoginRequest struct {
	AccountName string   `json:"accountName"`
	Password    string   `json:"password"`
	RememberMe  bool     `json:"rememberMe"`
	TrustTokens []string `json:"trustTokens"`
}

type LoginResponse struct {
	AuthType string `json:"authType"`
}

func (drive *Drive) validate2FA(code string, sessionData *SessionData) error {
	payload := ValidateCodeRequest{
		SecurityCode: SecurityCode{
			Code: code,
		},
	}
	buf := new(bytes.Buffer)
	json.NewEncoder(buf).Encode(payload)
	req, err := http.NewRequest("POST", "https://idmsa.apple.com/appleauth/auth/verify/trusteddevice/securitycode", buf)
	if err != nil {
		return err
	}
	req.Header.Add("Origin", "https://www.icloud.com")
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("Accept", "application/json")

	req.Header.Add("Scnt", sessionData.Scnt)
	req.Header.Add("X-Apple-ID-Session-Id", sessionData.SessionId)
	req.Header.Add("X-Apple-Widget-Key", "d39ba9916b7251055b22c7f910e2ea796ee65e98b2ddecea8f5dde8d9d1a815d")

	resp, err := drive.client.Do(req)
	if err != nil {
		return err
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	response := string(body)
	if len(response) == 0 {
		return nil
	} else {
		return fmt.Errorf("Error when validating 2fa code: %v", response)
	}
}

type SecurityCode struct {
	Code string `json:"code"`
}

type ValidateCodeRequest struct {
	SecurityCode SecurityCode `json:"securityCode"`
}

func (drive *Drive) trustSession(sessionData *SessionData) (*SessionData, error) {
	req, err := http.NewRequest("GET", "https://idmsa.apple.com/appleauth/auth/2sv/trust", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Add("Origin", "https://www.icloud.com")
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("Accept", "application/json")

	req.Header.Add("Scnt", sessionData.Scnt)
	req.Header.Add("X-Apple-ID-Session-Id", sessionData.SessionId)
	req.Header.Add("X-Apple-Widget-Key", "d39ba9916b7251055b22c7f910e2ea796ee65e98b2ddecea8f5dde8d9d1a815d")
	req.Header.Add("X-Apple-Session-Token", sessionData.SessionToken)

	resp, err := drive.client.Do(req)
	if err != nil {
		return nil, err
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	response := string(body)
	if len(response) == 0 {
		return newSessionData(resp.Header)
	} else {
		return nil, fmt.Errorf("Error when validating 2fa code: %v", response)
	}
}

func (drive *Drive) authenticate(sessionData SessionData) (bool, error) {
	payload := AuthenticateRequest{
		AccountCountyCode: sessionData.AccountCountyCode,
		DSWebAuthToken:    sessionData.SessionToken,
		TrustTokens:       []string{sessionData.TwoFactorToken},
		ExtendedLogin:     true,
	}
	buf := new(bytes.Buffer)
	json.NewEncoder(buf).Encode(payload)
	req, err := http.NewRequest("POST", "https://setup.icloud.com/setup/ws/1/accountLogin", buf)
	if err != nil {
		return false, err
	}
	req.Header.Add("Origin", "https://www.icloud.com")
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("Accept", "application/json")

	resp, err := drive.client.Do(req)
	if err != nil {
		return false, err
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}
	if resp.StatusCode != 200 {
		return false, fmt.Errorf("Incorrect status code: %v", resp.StatusCode)
	}
	response := new(TokenResponse)
	json.Unmarshal(body, &response)
	if response == nil {
		return false, fmt.Errorf("Unable to authenticate with token")
	}
	requires2FA := response.DsInfo.HSAVersion == 2 && response.HSAChallengeRequired
	return requires2FA, nil
}

type AuthenticateRequest struct {
	AccountCountyCode string   `json:"accountCountryCode"`
	DSWebAuthToken    string   `json:"dsWebAuthToken"`
	TrustTokens       []string `json:"trustTokens"`
	ExtendedLogin     bool     `json:"extended_login"`
}

func (drive *Drive) ValidateToken() error {
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
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	response := new(TokenResponse)
	json.Unmarshal(body, &response)
	if response == nil {
		return fmt.Errorf("Unable to validate token")
	}
	if response.DsInfo == nil {
		return fmt.Errorf("Error when validating token: %v", string(body))
	}
	log.Println("Validated token for:", response.DsInfo.PrimaryEmail)
	return nil
}

type TokenResponse struct {
	Error                *int    `json:"error"`
	DsInfo               *DsInfo `json:"dsInfo"`
	HSAChallengeRequired bool    `json:"hsaChallengeRequired"`
}

type DsInfo struct {
	PrimaryEmail string `json:"primaryEmail"`
	HSAVersion   int    `json:"hsaVersion"`
}

func (drive *Drive) GetRootNode() (*Node, error) {
	return drive.getNodeData("FOLDER::com.apple.CloudDocs::root")
}

func (drive *Drive) GetNodeData(node *Node) (*Node, error) {
	// This is a proxy for if this node already has all data, or if we need to fetch it to get children etc.
	if node.shallow {
		return drive.RefreshNodeData(node)
	} else {
		return node, nil
	}
}

func (drive *Drive) RefreshNodeData(node *Node) (*Node, error) {
	data, err := drive.getNodeData(node.drivewsid)
	if err != nil {
		return nil, err
	}
	node.setChildren(data.children)
	return node, nil
}

func (drive *Drive) getNodeData(drivewsid string) (*Node, error) {
	payload := []GetNodeDataRequest{
		{
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
	body, err := io.ReadAll(resp.Body)
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

	// TODO: For folders, use max(DateChanged) from children as DateChanged?
	parent := &Node{
		drivewsid:   node.Drivewsid,
		docwsid:     node.Docwsid,
		zone:        node.Zone,
		shallow:     false,
		Name:        node.Name,
		Size:        node.Size,
		Extension:   node.Extension,
		Etag:        node.Etag,
		DateCreated: node.DateCreated,
	}
	var children []Node
	for _, item := range node.Items {
		children = append(children, Node{
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

func (node *Node) setChildren(children *[]Node) {
	if children == nil {
		return
	}
	for idx := range *children {
		(*children)[idx].parent = node
	}
	node.children = children
	node.shallow = false
}

// This tries to make sure that we dont keep stale references cached.
// It does so by enumerating recent docs, which gives us a marker that we can then poll until iCloud tells us things have changed.
// When this happens we get a new marker, and returns true, so that other parts of the package can re-fetch data.
//
// This method was derived from observing what's happening on iCloud.com, and is indeed very crude, but seems to do the trick.
func (drive *Drive) CheckIfHasNewChanges() (bool, error) {
	var hasChanges bool
	var err error
	if drive.continuationMarker != nil {
		hasChanges, err = drive.checkHasChanges(*drive.continuationMarker)
		if err != nil {
			return false, err
		}
		if hasChanges {
			drive.continuationMarker = nil
		}
	}

	if drive.continuationMarker == nil {
		enumerate, err := drive.enumerateRecentDocs()
		if err != nil {
			return false, err
		}
		drive.continuationMarker = &enumerate.ContinuationMarker
	}
	return hasChanges, nil
}

func (drive *Drive) checkHasChanges(continuationMarker string) (bool, error) {
	url := fmt.Sprintf("https://p63-docws.icloud.com/ws/_all_/list/changes/recentDocs?limit=50&nextPage=%s", url.QueryEscape(continuationMarker))
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return false, err
	}
	req.Header.Add("Origin", "https://www.icloud.com")
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("Accept", "application/json")
	resp, err := drive.client.Do(req)
	if err != nil {
		return false, err
	}
	if resp.StatusCode == 205 {
		return true, nil
	} else {
		return false, nil
	}
}

func (drive *Drive) enumerateRecentDocs() (*EnumerateResponse, error) {
	req, err := http.NewRequest("GET", "https://p63-docws.icloud.com/ws/_all_/list/enumerate/recentDocs?limit=50", nil)
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
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	response := new(EnumerateResponse)
	err = json.Unmarshal(body, &response)
	return response, err
}

type EnumerateResponse struct {
	ContinuationMarker string `json:"continuationMarker"`
}

func (drive *Drive) GetChildren(node *Node) (*[]Node, error) {
	node, err := drive.GetNodeData(node)
	if err != nil {
		return nil, err
	}
	return node.children, nil
}

func (drive *Drive) GetNode(path string) (*Node, error) {
	node, err := drive.GetRootNode()
	if err != nil {
		return nil, err
	}
	for _, component := range strings.Split(path, "/") {
		if component == "" {
			continue
		}
		var child *Node
		for i, candidate := range *node.children {
			if candidate.Filename() == component {
				child, err = drive.GetNodeData(&candidate)
				if err != nil {
					return nil, err
				}
				if child != nil {
					(*node.children)[i] = *child
				}
				break
			}
		}
		if child == nil {
			return nil, fmt.Errorf("Could not find component: %s", component)
		}
		node = child
	}
	return node, nil
}

func (drive *Drive) GetData(node *Node) ([]byte, error) {
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
	body, err := io.ReadAll(resp.Body)
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
	return io.ReadAll(resp.Body)
}

func (drive *Drive) WriteData(node *Node, data []byte) error {
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

	body, err := io.ReadAll(resp.Body)
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
	return nil
}

func (drive *Drive) uploadFileData(node *Node) (*UploadURLResponse, error) {
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
	body, err := io.ReadAll(resp.Body)
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

func (drive *Drive) updateDocumentLink(node *Node, fileData UploadFileData) error {
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
	Drivewsid   string    `json:"drivewsid"`
	Docwsid     string    `json:"docwsid"`
	Zone        string    `json:"zone"`
	Name        string    `json:"name"`
	Size        uint64    `json:"size"`
	Type        string    `json:"type"`
	Extension   *string   `json:"extension"`
	Etag        string    `json:"etag"`
	DateCreated time.Time `json:"dateCreated"`

	Items []NodeDataItem `json:"items"`
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

type Node struct {
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

	parent   *Node
	children *[]Node
}

func (node *Node) Hash() uint64 {
	h := fnv.New64a()
	h.Write([]byte(node.drivewsid))
	return h.Sum64()
}

func (node *Node) Filename() string {
	if node.Extension != nil {
		return fmt.Sprintf("%s.%s", node.Name, *node.Extension)
	} else {
		return node.Name
	}
}
