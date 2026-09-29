package azblob

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blockblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
)

type sdkAPI struct {
	client *container.Client
}

func newSDKAPI(accountURL, connectionString, containerName string) (*sdkAPI, error) {
	opts := &container.ClientOptions{ClientOptions: policy.ClientOptions{
		Retry: policy.RetryOptions{TryTimeout: requestTimeout},
	}}

	if connectionString != "" {
		client, err := container.NewClientFromConnectionString(connectionString, containerName, opts)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", connectionStringEnv, err)
		}
		return &sdkAPI{client: client}, nil
	}

	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("loading azure credentials: %w", err)
	}
	containerURL := strings.TrimSuffix(accountURL, "/") + "/" + containerName
	client, err := container.NewClient(containerURL, cred, opts)
	if err != nil {
		return nil, err
	}
	return &sdkAPI{client: client}, nil
}

func (a *sdkAPI) Download(ctx context.Context, name string) ([]byte, string, error) {
	resp, err := a.client.NewBlobClient(name).DownloadStream(ctx, nil)
	if err != nil {
		if bloberror.HasCode(err, bloberror.BlobNotFound) {
			return nil, "", errNotFound
		}
		return nil, "", err
	}
	defer resp.Body.Close()

	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	if resp.ETag == nil {
		return nil, "", fmt.Errorf("no ETag in the response for %s", name)
	}
	return content, strings.Trim(string(*resp.ETag), `"`), nil
}

func (a *sdkAPI) Upload(ctx context.Context, name string, content []byte, ifMatch string, meta map[string]string) (string, string, error) {
	conditions := &blob.ModifiedAccessConditions{}
	if ifMatch != "" {
		etag := azcore.ETag(`"` + ifMatch + `"`)
		conditions.IfMatch = &etag
	} else {
		wildcard := azcore.ETagAny
		conditions.IfNoneMatch = &wildcard
	}

	contentType := "application/yaml"
	resp, err := a.client.NewBlockBlobClient(name).Upload(ctx, nopSeekCloser{bytes.NewReader(content)}, &blockblob.UploadOptions{
		Metadata:         toPointers(meta),
		HTTPHeaders:      &blob.HTTPHeaders{BlobContentType: &contentType},
		AccessConditions: &blob.AccessConditions{ModifiedAccessConditions: conditions},
	})
	if err != nil {
		if bloberror.HasCode(err, bloberror.ConditionNotMet, bloberror.BlobAlreadyExists) {
			return "", "", errPreconditionFailed
		}
		return "", "", err
	}

	etag, versionID := "", ""
	if resp.ETag != nil {
		etag = strings.Trim(string(*resp.ETag), `"`)
	}
	if resp.VersionID != nil {
		versionID = *resp.VersionID
	}
	return etag, versionID, nil
}

func (a *sdkAPI) List(ctx context.Context, prefix, delimiter string, versions bool) ([]Item, []string, error) {
	include := container.ListBlobsInclude{Versions: versions, Metadata: versions}
	var items []Item
	var prefixes []string

	if delimiter == "" {
		pager := a.client.NewListBlobsFlatPager(&container.ListBlobsFlatOptions{Prefix: &prefix, Include: include})
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return nil, nil, err
			}
			for _, bi := range page.Segment.BlobItems {
				items = append(items, toItem(bi))
			}
		}
		return items, nil, nil
	}

	pager := a.client.NewListBlobsHierarchyPager(delimiter, &container.ListBlobsHierarchyOptions{Prefix: &prefix, Include: include})
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, nil, err
		}
		for _, bi := range page.Segment.BlobItems {
			items = append(items, toItem(bi))
		}
		for _, bp := range page.Segment.BlobPrefixes {
			if bp.Name != nil {
				prefixes = append(prefixes, *bp.Name)
			}
		}
	}
	return items, prefixes, nil
}

func toItem(bi *container.BlobItem) Item {
	item := Item{Metadata: map[string]string{}}
	if bi.Name != nil {
		item.Name = *bi.Name
	}
	if bi.VersionID != nil {
		item.VersionID = *bi.VersionID
	}
	if bi.Properties != nil && bi.Properties.LastModified != nil {
		item.Modified = *bi.Properties.LastModified
	}
	for k, v := range bi.Metadata {
		if v != nil {
			item.Metadata[k] = *v
		}
	}
	return item
}

func toPointers(meta map[string]string) map[string]*string {
	if len(meta) == 0 {
		return nil
	}
	out := make(map[string]*string, len(meta))
	for k, v := range meta {
		out[k] = &v
	}
	return out
}

type nopSeekCloser struct{ *bytes.Reader }

func (nopSeekCloser) Close() error { return nil }
