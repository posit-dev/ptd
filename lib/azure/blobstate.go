package azure

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
)

// BlobStateReader reads Pulumi state files from Azure Blob Storage.
// It reuses a single azblob client for all operations.
type BlobStateReader struct {
	client    *azblob.Client
	container string
}

// NewBlobStateReader creates a BlobStateReader for the given storage account and container.
func NewBlobStateReader(creds *Credentials, storageAccountName string, containerName string) (*BlobStateReader, error) {
	serviceURL := fmt.Sprintf("https://%s.blob.core.windows.net", storageAccountName)
	client, err := azblob.NewClient(serviceURL, creds.AzureCredential(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create blob client: %w", err)
	}
	return &BlobStateReader{client: client, container: containerName}, nil
}

// ListStateFiles lists all .json Pulumi state files in the container.
func (r *BlobStateReader) ListStateFiles(ctx context.Context) ([]string, error) {
	prefix := ".pulumi/stacks/"
	var keys []string

	pager := r.client.NewListBlobsFlatPager(r.container, &azblob.ListBlobsFlatOptions{
		Prefix: &prefix,
	})

	for pager.More() {
		resp, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list blobs: %w", err)
		}
		for _, blob := range resp.Segment.BlobItems {
			if blob.Name == nil {
				continue
			}
			name := *blob.Name
			if strings.HasSuffix(name, ".json") && !strings.HasSuffix(name, ".json.bak") {
				keys = append(keys, name)
			}
		}
	}

	return keys, nil
}

// GetStateFile downloads a Pulumi state file and returns its contents.
func (r *BlobStateReader) GetStateFile(ctx context.Context, blobName string) ([]byte, error) {
	resp, err := r.client.DownloadStream(ctx, r.container, blobName, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to download blob %s: %w", blobName, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read blob body: %w", err)
	}

	return data, nil
}
