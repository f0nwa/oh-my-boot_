package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"pxe-platform/internal/agent"
	"pxe-platform/internal/boot"
	"pxe-platform/internal/drivers"
	"pxe-platform/internal/images"
	"pxe-platform/internal/network"
)

type BootStatus struct {
	Ready  bool   `json:"ready"`
	SHA256 string `json:"sha256"`
}
type AgentStatus struct {
	Applied   bool   `json:"applied"`
	Mode      string `json:"mode"`
	Interface string `json:"interface"`
}
type BootClient struct{ http *http.Client }
type AgentClient struct{ http *http.Client }
type ImagesClient struct{ http *http.Client }

func socketHTTP(path string) *http.Client {
	return &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}}}
}
func NewBootClient(path string) *BootClient { return &BootClient{http: socketHTTP(path)} }
func NewAgentClient(path string) *AgentClient {
	client := socketHTTP(path)
	client.Timeout = 5 * time.Minute
	return &AgentClient{http: client}
}

func (c *AgentClient) ImportDriverPackage(ctx context.Context, body io.Reader, size int64) (drivers.Inventory, error) {
	var inventory drivers.Inventory
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unix/winpe-drivers/import", io.NopCloser(body))
	if err != nil {
		return inventory, err
	}
	req.ContentLength = size
	client := &http.Client{Transport: c.http.Transport, Timeout: 10 * time.Minute}
	res, err := client.Do(req)
	if err != nil {
		return inventory, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		message, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
		return inventory, fmt.Errorf("driver import: %d %s", res.StatusCode, string(message))
	}
	err = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&inventory)
	return inventory, err
}

func (c *AgentClient) CheckDriverPackage(ctx context.Context, hash string) error {
	body, err := json.Marshal(map[string]string{"sha256": hash})
	if err != nil {
		return err
	}
	return localCall(ctx, c.http, "POST", "/winpe-drivers/check", body, nil)
}

func (c *AgentClient) PrepareDriverConfig(ctx context.Context, req agent.DriverBuildRequest) (agent.DriverBuildResult, error) {
	var result agent.DriverBuildResult
	body, err := json.Marshal(req)
	if err != nil {
		return result, err
	}
	client := &http.Client{Transport: c.http.Transport, Timeout: 15 * time.Minute}
	err = localCall(ctx, client, "POST", "/winpe-drivers/prepare", body, &result)
	return result, err
}

func (c *AgentClient) CheckDriverConfig(ctx context.Context, check agent.DriverBuildCheck) error {
	body, err := json.Marshal(check)
	if err != nil {
		return err
	}
	client := &http.Client{Transport: c.http.Transport, Timeout: 5 * time.Minute}
	return localCall(ctx, client, "POST", "/winpe-drivers/check-config", body, nil)
}

func (c *AgentClient) RemoveDriverConfig(ctx context.Context, id int64, key string, removeWIM bool) error {
	body, err := json.Marshal(struct {
		ID        int64  `json:"id"`
		WIMKey    string `json:"wim_key"`
		RemoveWIM bool   `json:"remove_wim"`
	}{id, key, removeWIM})
	if err != nil {
		return err
	}
	return localCall(ctx, c.http, "POST", "/winpe-drivers/remove-config", body, nil)
}

func (c *AgentClient) DriverConfigWIMKey(ctx context.Context, id int64) (string, error) {
	var result struct {
		WIMKey string `json:"wim_key"`
	}
	err := localCall(ctx, c.http, "GET", "/winpe-drivers/config-key/"+strconv.FormatInt(id, 10), nil, &result)
	return result.WIMKey, err
}

func (c *AgentClient) RemoveWindowsProfile(ctx context.Context, id int64) error {
	body, err := json.Marshal(map[string]int64{"id": id})
	if err != nil {
		return err
	}
	return localCall(ctx, c.http, "POST", "/windows-iso/remove", body, nil)
}

func (c *AgentClient) RemoveWindowsAnswer(ctx context.Context, id int64) error {
	body, err := json.Marshal(map[string]int64{"id": id})
	if err != nil {
		return err
	}
	return localCall(ctx, c.http, "POST", "/windows-answer/remove", body, nil)
}

func (c *AgentClient) PrepareWindowsISO(ctx context.Context, req agent.WindowsISORequest) (agent.WindowsISOResult, error) {
	var result agent.WindowsISOResult
	body, err := json.Marshal(req)
	if err != nil {
		return result, err
	}
	err = localCall(ctx, c.http, "POST", "/windows-iso/prepare", body, &result)
	return result, err
}
func (c *AgentClient) CheckWindowsISO(ctx context.Context, req agent.WindowsISORequest) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	return localCall(ctx, c.http, "POST", "/windows-iso/check", body, nil)
}
func (c *AgentClient) PrepareWindowsAnswer(ctx context.Context, req agent.WindowsAnswerRequest) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	return localCall(ctx, c.http, "POST", "/windows-answer/prepare", body, nil)
}
func (c *AgentClient) CheckWindowsAnswer(ctx context.Context, req agent.WindowsAnswerCheck) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	return localCall(ctx, c.http, "POST", "/windows-answer/check", body, nil)
}
func NewImagesClient(path string) *ImagesClient { return &ImagesClient{http: socketHTTP(path)} }

func (c *ImagesClient) Catalog(ctx context.Context) (images.Catalog, error) {
	var v images.Catalog
	err := localCall(ctx, c.http, "GET", "/catalog", nil, &v)
	return v, err
}

func (c *ImagesClient) Folders(ctx context.Context, id int64) ([]string, error) {
	var folders []string
	client := &http.Client{Transport: c.http.Transport, Timeout: 30 * time.Second}
	err := localCall(ctx, client, "GET", "/smb-storages/"+strconv.FormatInt(id, 10)+"/folders", nil, &folders)
	return folders, err
}

func (c *ImagesClient) UploadISO(ctx context.Context, id int64, folder, name string, body io.Reader, size int64) (images.UploadResult, error) {
	var result images.UploadResult
	query := url.Values{"folder": {folder}, "name": {name}}
	path := "/smb-storages/" + strconv.FormatInt(id, 10) + "/upload?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unix"+path, io.NopCloser(body))
	if err != nil {
		return result, err
	}
	req.ContentLength = size
	client := &http.Client{Transport: c.http.Transport}
	res, err := client.Do(req)
	if err != nil {
		return result, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		message, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
		return result, fmt.Errorf("image upload: %d %s", res.StatusCode, string(message))
	}
	err = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&result)
	return result, err
}

func (c *ImagesClient) Scan(ctx context.Context, id int64) error {
	body, err := json.Marshal(map[string]int64{"storage_id": id})
	if err != nil {
		return err
	}
	return localCall(ctx, c.http, "POST", "/scan", body, nil)
}

func (c *ImagesClient) CreateSMB(ctx context.Context, input images.ManagedStorageInput) (images.Storage, error) {
	var storage images.Storage
	body, err := json.Marshal(input)
	if err != nil {
		return storage, err
	}
	err = localCall(ctx, c.http, "POST", "/smb-storages", body, &storage)
	return storage, err
}

func (c *ImagesClient) MarkSMBError(ctx context.Context, id int64) error {
	return localCall(ctx, c.http, "POST", "/smb-storages/"+strconv.FormatInt(id, 10)+"/error", nil, nil)
}

func (c *ImagesClient) MarkSMBScanError(ctx context.Context, id int64) error {
	return localCall(ctx, c.http, "POST", "/smb-storages/"+strconv.FormatInt(id, 10)+"/scan-error", nil, nil)
}

func (c *ImagesClient) DeleteSMB(ctx context.Context, id int64) error {
	return localCall(ctx, c.http, "DELETE", "/smb-storages/"+strconv.FormatInt(id, 10), nil, nil)
}

func (c *AgentClient) Mount(ctx context.Context, key, server, share, subpath, username, password, domain string) error {
	body, err := json.Marshal(map[string]string{"key": key, "server": server, "share": share, "subpath": subpath, "username": username, "password": password, "domain": domain})
	if err != nil {
		return err
	}
	return localCall(ctx, c.http, "POST", "/smb/mount", body, nil)
}

func (c *AgentClient) EnableUpload(ctx context.Context, key, server, share, subpath string) error {
	body, err := json.Marshal(map[string]string{"key": key, "server": server, "share": share, "subpath": subpath})
	if err != nil {
		return err
	}
	return localCall(ctx, c.http, "POST", "/smb/enable-upload", body, nil)
}

func (c *AgentClient) Remove(ctx context.Context, key string) error {
	body, err := json.Marshal(map[string]string{"key": key})
	if err != nil {
		return err
	}
	return localCall(ctx, c.http, "POST", "/smb/remove", body, nil)
}

func localCall(ctx context.Context, client *http.Client, method, path string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, "http://unix"+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
		return fmt.Errorf("local API %s: %d %s", path, res.StatusCode, string(message))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(out)
}

func (c *BootClient) Status(ctx context.Context) (BootStatus, error) {
	var v BootStatus
	err := localCall(ctx, c.http, "GET", "/status", nil, &v)
	return v, err
}
func (c *BootClient) CurrentMenu(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "http://unix/menu", nil)
	if err != nil {
		return nil, err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("boot menu unavailable: %d", res.StatusCode)
	}
	return io.ReadAll(io.LimitReader(res.Body, 256<<10+1))
}
func (c *BootClient) Events(ctx context.Context, limit int) ([]boot.Event, error) {
	if limit < 1 || limit > 100 {
		return nil, fmt.Errorf("invalid event limit")
	}
	var v []boot.Event
	err := localCall(ctx, c.http, "GET", "/events?limit="+strconv.Itoa(limit), nil, &v)
	return v, err
}
func (c *BootClient) Publish(ctx context.Context, script []byte) (string, error) {
	if len(script) > 256<<10 {
		return "", fmt.Errorf("menu too large")
	}
	var v struct {
		SHA256 string `json:"sha256"`
	}
	err := localCall(ctx, c.http, "POST", "/publish", script, &v)
	return v.SHA256, err
}
func (c *AgentClient) Status(ctx context.Context) (AgentStatus, error) {
	var v AgentStatus
	err := localCall(ctx, c.http, "GET", "/status", nil, &v)
	return v, err
}
func (c *AgentClient) Config(ctx context.Context) (network.Config, error) {
	var v network.Config
	err := localCall(ctx, c.http, "GET", "/config", nil, &v)
	return v, err
}
func (c *AgentClient) Apply(ctx context.Context, v network.Config) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return localCall(ctx, c.http, "POST", "/apply", payload, nil)
}
