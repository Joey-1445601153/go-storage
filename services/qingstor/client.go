package qingstor

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	iface "github.com/yunify/qingstor-sdk-go/v3/interface"
	"github.com/yunify/qingstor-sdk-go/v3/service"

	"github.com/Xuanwo/storage/pkg/iterator"
	"github.com/Xuanwo/storage/pkg/segment"
	"github.com/Xuanwo/storage/types"
)

// Client is the qingstor object storage client.
//
//go:generate go run ../../internal/cmd/meta_gen/main.go
//go:generate mockgen -package qingstor -destination mock_test.go github.com/yunify/qingstor-sdk-go/v3/interface Service,Bucket
type Client struct {
	config  *Config
	service iface.Service
	bucket  iface.Bucket

	segments map[string]*segment.Segment
}

// setupBucket will setup bucket for client.
func (c *Client) setupBucket(bucketName, zoneName string) (err error) {
	errorMessage := "setup qingstor bucket failed: %w"

	if zoneName != "" {
		bucket, err := c.service.Bucket(bucketName, zoneName)
		if err != nil {
			return handleError(fmt.Errorf(errorMessage, err))
		}
		c.bucket = bucket
		return nil
	}

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	url := fmt.Sprintf("%s://%s.%s:%d", c.config.Protocol, bucketName, c.config.Host, c.config.Port)

	r, err := client.Head(url)
	if err != nil {
		return handleError(fmt.Errorf(errorMessage, err))
	}
	if r.StatusCode != http.StatusTemporaryRedirect {
		err = fmt.Errorf("head status is %d instead of %d", r.StatusCode, http.StatusTemporaryRedirect)
		return handleError(fmt.Errorf(errorMessage, err))
	}

	// Example URL: https://bucket.zone.qingstor.com
	zoneName = strings.Split(r.Header.Get("Location"), ".")[1]
	bucket, err := c.service.Bucket(bucketName, zoneName)
	if err != nil {
		return handleError(fmt.Errorf(errorMessage, err))
	}
	c.bucket = bucket
	return
}

// Stat implements Storager.Stat
func (c *Client) Stat(path string, opt ...*types.Pair) (o *types.Object, err error) {
	errorMessage := "qingstor Stat failed: %w"

	input := &service.HeadObjectInput{}

	output, err := c.bucket.HeadObject(path, input)
	if err != nil {
		return nil, handleError(fmt.Errorf(errorMessage, err))
	}

	o = &types.Object{
		Name:     path,
		Type:     types.ObjectTypeFile,
		Metadata: make(types.Metadata),
	}
	o.SetType(service.StringValue(output.ContentType))
	o.SetSize(*output.ContentLength)
	o.SetChecksum(service.StringValue(output.ETag))
	o.SetStorageClass(service.StringValue(output.XQSStorageClass))
	return o, nil

}

// Delete implements Storager.Delete
func (c *Client) Delete(path string, opt ...*types.Pair) (err error) {
	errorMessage := "qingstor Delete failed: %w"

	// TODO: support delete dir.

	_, err = c.bucket.DeleteObject(path)
	if err != nil {
		return handleError(fmt.Errorf(errorMessage, err))
	}
	return nil
}

// Copy implements Storager.Copy
func (c *Client) Copy(src, dst string, option ...*types.Pair) (err error) {
	errorMessage := "qingstor Copy failed: %w"

	_, err = c.bucket.PutObject(dst, &service.PutObjectInput{
		XQSCopySource: &src,
	})
	if err != nil {
		return handleError(fmt.Errorf(errorMessage, err))
	}
	return nil
}

// Move implements Storager.Move
func (c *Client) Move(src, dst string, option ...*types.Pair) (err error) {
	errorMessage := "qingstor Move failed: %w"

	_, err = c.bucket.PutObject(dst, &service.PutObjectInput{
		XQSMoveSource: &src,
	})
	if err != nil {
		return handleError(fmt.Errorf(errorMessage, err))
	}
	return nil
}

// Reach implements Storager.Reach
func (c *Client) Reach(path string, pairs ...*types.Pair) (url string, err error) {
	errorMessage := "qingstor Reach failed: %w"

	// FIXME: sdk should export GetObjectRequest as interface too?
	bucket := c.bucket.(*service.Bucket)

	r, _, err := bucket.GetObjectRequest(path, nil)
	if err != nil {
		return "", handleError(fmt.Errorf(errorMessage, err))
	}
	if err = r.Build(); err != nil {
		return "", handleError(fmt.Errorf(errorMessage, err))
	}
	// TODO: support set expire via pair.
	if err = r.SignQuery(3600); err != nil {
		return "", handleError(fmt.Errorf(errorMessage, err))
	}
	return r.HTTPRequest.URL.String(), nil
}

// CreateDir implements Storager.CreateDir
func (c *Client) CreateDir(path string, option ...*types.Pair) (err error) {
	errorMessage := "qingstor CreateDir failed: %w"

	opt := parsePairCreateDir(option...)
	if !opt.HasLocation {
		// TODO: return location missing error.
		panic("missing value")
	}

	bucket, err := c.service.Bucket(path, opt.Location)
	if err != nil {
		return handleError(fmt.Errorf(errorMessage, err))
	}

	_, err = bucket.Put()
	if err != nil {
		return handleError(fmt.Errorf(errorMessage, err))
	}
	return
}

// ListDir implements Storager.ListDir
func (c *Client) ListDir(path string, opt ...*types.Pair) (it iterator.Iterator) {
	errorMessage := "qingstor ListDir failed: %w"

	marker := ""
	limit := 200

	var output *service.ListObjectsOutput
	var err error

	fn := iterator.NextFunc(func(informer *[]*types.Object) error {
		idx := 0
		buf := make([]*types.Object, limit)

		output, err = c.bucket.ListObjects(&service.ListObjectsInput{
			Limit:  &limit,
			Marker: &marker,
			Prefix: &path,
		})
		if err != nil {
			return handleError(fmt.Errorf(errorMessage, err))
		}

		for _, v := range output.Keys {
			o := &types.Object{
				Name:     *v.Key,
				Type:     types.ObjectTypeFile,
				Metadata: make(types.Metadata),
			}
			o.SetType(service.StringValue(v.MimeType))
			o.SetStorageClass(service.StringValue(v.StorageClass))
			o.SetChecksum(service.StringValue(v.Etag))

			buf[idx] = o
			idx++
		}

		marker = *output.NextMarker
		if marker == "" {
			return iterator.ErrDone
		}
		if output.HasMore != nil && !*output.HasMore {
			return iterator.ErrDone
		}
		if len(output.Keys) == 0 {
			return iterator.ErrDone
		}
		return nil
	})

	it = iterator.NewPrefixBasedIterator(fn)
	return
}

// Read implements Storager.Read
func (c *Client) Read(path string, option ...*types.Pair) (r io.ReadCloser, err error) {
	errorMessage := "qingstor ReadFile failed: %w"

	_ = parsePairRead(option...)
	input := &service.GetObjectInput{}

	output, err := c.bucket.GetObject(path, input)
	if err != nil {
		return nil, handleError(fmt.Errorf(errorMessage, err))
	}
	return output.Body, nil
}

// WriteFile implements Storager.WriteFile
func (c *Client) WriteFile(path string, size int64, r io.Reader, option ...*types.Pair) (err error) {
	errorMessage := "qingstor WriteFile for path %s failed: %w"

	opts := parsePairWriteFile(option...)
	input := &service.PutObjectInput{
		ContentLength: &size,
		Body:          r,
	}
	if opts.HasChecksum {
		input.ContentMD5 = &opts.Checksum
	}
	if opts.HasStorageClass {
		input.XQSStorageClass = &opts.StorageClass
	}

	_, err = c.bucket.PutObject(path, input)
	if err != nil {
		return handleError(fmt.Errorf(errorMessage, path, err))
	}
	return nil
}

// WriteStream implements Storager.WriteStream
func (c *Client) WriteStream(path string, r io.Reader, option ...*types.Pair) (err error) {
	panic("not supported")
}

// InitSegment implements Storager.InitSegment
func (c *Client) InitSegment(path string, option ...*types.Pair) (err error) {
	errorMessage := "qingstor InitSegment for path %s failed: %w"

	if _, ok := c.segments[path]; ok {
		return handleError(fmt.Errorf(errorMessage, path, segment.ErrSegmentAlreadyInitiated))
	}

	_ = parsePairInitSegment(option...)
	input := &service.InitiateMultipartUploadInput{}

	output, err := c.bucket.InitiateMultipartUpload(path, input)
	if err != nil {
		return handleError(fmt.Errorf(errorMessage, path, err))
	}

	c.segments[path] = &segment.Segment{
		ID:    *output.UploadID,
		Parts: make([]*segment.Part, 0),
	}
	return
}

// ReadSegment implements Storager.ReadSegment
func (c *Client) ReadSegment(path string, offset, size int64, option ...*types.Pair) (r io.ReadCloser, err error) {
	panic("implement me")
}

// WriteSegment implements Storager.WriteSegment
func (c *Client) WriteSegment(path string, offset, size int64, r io.Reader, option ...*types.Pair) (err error) {
	errorMessage := "qingstor WriteSegment for path %s failed: %w"

	s, ok := c.segments[path]
	if !ok {
		return handleError(fmt.Errorf(errorMessage, path, segment.ErrSegmentAlreadyInitiated))
	}

	p := &segment.Part{
		Offset: offset,
		Size:   size,
	}

	partNumber, err := s.GetPartIndex(p)
	if err != nil {
		return handleError(fmt.Errorf(errorMessage, path, err))
	}

	_, err = c.bucket.UploadMultipart(path, &service.UploadMultipartInput{
		PartNumber:    &partNumber,
		UploadID:      &s.ID,
		ContentLength: &size,
		Body:          r,
	})
	if err != nil {
		return handleError(fmt.Errorf(errorMessage, path, err))
	}

	err = s.InsertPart(p)
	if err != nil {
		return handleError(fmt.Errorf(errorMessage, path, err))
	}
	return
}

// CompleteSegment implements Storager.CompleteSegment
func (c *Client) CompleteSegment(path string, option ...*types.Pair) (err error) {
	errorMessage := "qingstor CompleteSegment for path %s failed: %w"

	s, ok := c.segments[path]
	if !ok {
		return handleError(fmt.Errorf(errorMessage, path, segment.ErrSegmentNotInitiated))
	}

	err = s.ValidateParts()
	if err != nil {
		return
	}

	objectParts := make([]*service.ObjectPartType, len(s.Parts))
	for k, v := range s.Parts {
		partNumber := k
		objectParts[k] = &service.ObjectPartType{
			PartNumber: &partNumber,
			Size:       &v.Size,
		}
	}

	_, err = c.bucket.CompleteMultipartUpload(path, &service.CompleteMultipartUploadInput{
		UploadID:    &s.ID,
		ObjectParts: objectParts,
	})
	if err != nil {
		return handleError(fmt.Errorf(errorMessage, path, err))
	}

	delete(c.segments, path)
	return
}

// AbortSegment implements Storager.AbortSegment
func (c *Client) AbortSegment(path string, option ...*types.Pair) (err error) {
	errorMessage := "qingstor AbortSegment for path %s failed: %w"

	s, ok := c.segments[path]
	if !ok {
		return handleError(fmt.Errorf(errorMessage, path, segment.ErrSegmentNotInitiated))
	}

	_, err = c.bucket.AbortMultipartUpload(path, &service.AbortMultipartUploadInput{
		UploadID: &s.ID,
	})
	if err != nil {
		return handleError(fmt.Errorf(errorMessage, path, err))
	}

	delete(c.segments, path)
	return
}