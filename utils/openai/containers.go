package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// ContainerExpiration expires a container relative to its last activity.
type ContainerExpiration struct {
	Anchor  string `json:"anchor"`
	Minutes int    `json:"minutes"`
}

// CreateContainerParams is the native body for POST /containers.
type CreateContainerParams struct {
	Name          string               `json:"name"`
	ExpiresAfter  *ContainerExpiration `json:"expires_after,omitempty"`
	FileIDs       []string             `json:"file_ids,omitempty"`
	MemoryLimit   *string              `json:"memory_limit,omitempty"`
	NetworkPolicy any                  `json:"network_policy,omitempty"`
	Extra         map[string]any       `json:"-"`
}

// MarshalJSON adds forward-compatible native fields and rejects collisions
// with typed fields that are present.
func (p CreateContainerParams) MarshalJSON() ([]byte, error) {
	type alias CreateContainerParams
	return marshalFields(alias(p), p.Extra)
}

// ListContainersParams controls filtering, ordering, and cursor pagination.
type ListContainersParams struct {
	After *string
	Limit *int
	Name  *string
	Order *string
}

// Container is an OpenAI Code Interpreter container.
type Container struct {
	ID            string               `json:"id"`
	Object        string               `json:"object"`
	CreatedAt     int64                `json:"created_at"`
	Status        string               `json:"status"`
	ExpiresAfter  *ContainerExpiration `json:"expires_after,omitempty"`
	LastActiveAt  *int64               `json:"last_active_at,omitempty"`
	MemoryLimit   string               `json:"memory_limit,omitempty"`
	Name          string               `json:"name"`
	NetworkPolicy json.RawMessage      `json:"network_policy,omitempty"`
	HTTP          *HTTPResponse        `json:"-"`
}

// ContainerList is one page returned by ListContainers.
type ContainerList struct {
	Object  string        `json:"object"`
	Data    []Container   `json:"data"`
	FirstID string        `json:"first_id"`
	LastID  string        `json:"last_id"`
	HasMore bool          `json:"has_more"`
	HTTP    *HTTPResponse `json:"-"`
}

// ContainerDeletion is the deletion status returned by DeleteContainer.
type ContainerDeletion struct {
	ID      string        `json:"id"`
	Object  string        `json:"object"`
	Deleted bool          `json:"deleted"`
	HTTP    *HTTPResponse `json:"-"`
}

// AddContainerFileParams references an already uploaded global file.
type AddContainerFileParams struct {
	FileID string         `json:"file_id"`
	Extra  map[string]any `json:"-"`
}

// MarshalJSON adds forward-compatible native fields and rejects collisions.
func (p AddContainerFileParams) MarshalJSON() ([]byte, error) {
	type alias AddContainerFileParams
	return marshalFields(alias(p), p.Extra)
}

// ListContainerFilesParams controls ordering and cursor pagination.
type ListContainerFilesParams struct {
	After *string
	Limit *int
	Order *string
}

// ContainerFile is a file copied into or created inside a container.
type ContainerFile struct {
	ID          string        `json:"id"`
	Object      string        `json:"object"`
	Bytes       int64         `json:"bytes"`
	ContainerID string        `json:"container_id"`
	CreatedAt   int64         `json:"created_at"`
	Path        string        `json:"path"`
	Source      string        `json:"source"`
	HTTP        *HTTPResponse `json:"-"`
}

// ContainerFileList is one page returned by ListContainerFiles.
type ContainerFileList struct {
	Object  string          `json:"object"`
	Data    []ContainerFile `json:"data"`
	FirstID string          `json:"first_id"`
	LastID  string          `json:"last_id"`
	HasMore bool            `json:"has_more"`
	HTTP    *HTTPResponse   `json:"-"`
}

// ContainerFileDeletion is the status returned by DeleteContainerFile.
type ContainerFileDeletion struct {
	ID      string        `json:"id"`
	Object  string        `json:"object"`
	Deleted bool          `json:"deleted"`
	HTTP    *HTTPResponse `json:"-"`
}

// CreateContainer creates a container. It does not upload, retry, or delete
// any resources beyond the explicit file IDs in params.
func (c *Client) CreateContainer(ctx context.Context, params CreateContainerParams) (*Container, error) {
	result := new(Container)
	httpResponse, err := c.json(ctx, http.MethodPost, "/containers", params, result)
	result.HTTP = httpResponse
	return result, err
}

// ListContainers returns one native page and does not follow cursors.
func (c *Client) ListContainers(ctx context.Context, params ListContainersParams) (*ContainerList, error) {
	query := url.Values{}
	addStringQuery(query, "after", params.After)
	addIntQuery(query, "limit", params.Limit)
	addStringQuery(query, "name", params.Name)
	addStringQuery(query, "order", params.Order)
	result := new(ContainerList)
	httpResponse, err := c.json(ctx, http.MethodGet, withQuery("/containers", query), nil, result)
	result.HTTP = httpResponse
	return result, err
}

// GetContainer retrieves one container.
func (c *Client) GetContainer(ctx context.Context, containerID string) (*Container, error) {
	id, err := containerPathID(containerID)
	result := new(Container)
	if err != nil {
		return result, err
	}
	httpResponse, err := c.json(ctx, http.MethodGet, "/containers/"+id, nil, result)
	result.HTTP = httpResponse
	return result, err
}

// DeleteContainer explicitly deletes one container.
func (c *Client) DeleteContainer(ctx context.Context, containerID string) (*ContainerDeletion, error) {
	id, err := containerPathID(containerID)
	result := new(ContainerDeletion)
	if err != nil {
		return result, err
	}
	httpResponse, err := c.json(ctx, http.MethodDelete, "/containers/"+id, nil, result)
	result.HTTP = httpResponse
	return result, err
}

// AddContainerFile copies an existing global file into a container using JSON.
func (c *Client) AddContainerFile(ctx context.Context, containerID string, params AddContainerFileParams) (*ContainerFile, error) {
	container, err := containerPathID(containerID)
	result := new(ContainerFile)
	if err != nil {
		return result, err
	}
	httpResponse, err := c.json(ctx, http.MethodPost, "/containers/"+container+"/files", params, result)
	result.HTTP = httpResponse
	return result, err
}

// UploadContainerFile streams a new file into a container with multipart form data.
func (c *Client) UploadContainerFile(ctx context.Context, containerID string, upload Upload) (*ContainerFile, error) {
	container, err := containerPathID(containerID)
	result := new(ContainerFile)
	if err != nil {
		return result, err
	}
	upload.Field = "file"
	httpResponse, err := c.multipart(ctx, http.MethodPost, "/containers/"+container+"/files", nil, []Upload{upload}, result)
	result.HTTP = httpResponse
	return result, err
}

// ListContainerFiles returns one native page of files in a container.
func (c *Client) ListContainerFiles(ctx context.Context, containerID string, params ListContainerFilesParams) (*ContainerFileList, error) {
	container, err := containerPathID(containerID)
	result := new(ContainerFileList)
	if err != nil {
		return result, err
	}
	query := url.Values{}
	addStringQuery(query, "after", params.After)
	addIntQuery(query, "limit", params.Limit)
	addStringQuery(query, "order", params.Order)
	httpResponse, err := c.json(ctx, http.MethodGet, withQuery("/containers/"+container+"/files", query), nil, result)
	result.HTTP = httpResponse
	return result, err
}

// GetContainerFile retrieves metadata for one container file.
func (c *Client) GetContainerFile(ctx context.Context, containerID, fileID string) (*ContainerFile, error) {
	path, err := containerFilePath(containerID, fileID)
	result := new(ContainerFile)
	if err != nil {
		return result, err
	}
	httpResponse, err := c.json(ctx, http.MethodGet, path, nil, result)
	result.HTTP = httpResponse
	return result, err
}

// DeleteContainerFile explicitly deletes one file from a container.
func (c *Client) DeleteContainerFile(ctx context.Context, containerID, fileID string) (*ContainerFileDeletion, error) {
	path, err := containerFilePath(containerID, fileID)
	result := new(ContainerFileDeletion)
	if err != nil {
		return result, err
	}
	httpResponse, err := c.json(ctx, http.MethodDelete, path, nil, result)
	result.HTTP = httpResponse
	return result, err
}

// DownloadContainerFile streams a container file's raw content to dst.
func (c *Client) DownloadContainerFile(ctx context.Context, containerID, fileID string, dst io.Writer) (*HTTPResponse, error) {
	path, err := containerFilePath(containerID, fileID)
	if err != nil {
		return nil, err
	}
	return c.download(ctx, path+"/content", dst)
}

func containerPathID(containerID string) (string, error) {
	id, err := pathID(containerID)
	if err != nil {
		return "", fmt.Errorf("openai: container ID: %w", err)
	}
	return id, nil
}

func containerFilePath(containerID, fileID string) (string, error) {
	container, err := containerPathID(containerID)
	if err != nil {
		return "", err
	}
	file, err := pathID(fileID)
	if err != nil {
		return "", fmt.Errorf("openai: container file ID: %w", err)
	}
	return "/containers/" + container + "/files/" + file, nil
}
