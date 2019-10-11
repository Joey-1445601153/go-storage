package qingstor

import (
	"bytes"
	"errors"
	"io/ioutil"
	"os"
	"reflect"
	"testing"

	"bou.ke/monkey"
	"github.com/golang/mock/gomock"
	"github.com/google/uuid"
	"github.com/pengsrc/go-shared/convert"
	"github.com/stretchr/testify/assert"
	"github.com/yunify/qingstor-sdk-go/v3/service"

	"github.com/Xuanwo/storage/pkg/segment"
	"github.com/Xuanwo/storage/types"
)

func TestClient_AbortSegment(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockBucket := NewMockBucket(ctrl)

	client := Client{
		bucket:   mockBucket,
		segments: make(map[string]*segment.Segment),
	}

	// Test valid segment.
	path := uuid.New().String()
	client.segments[path] = &segment.Segment{
		ID: uuid.New().String(),
	}
	mockBucket.EXPECT().AbortMultipartUpload(gomock.Any(), gomock.Any()).Do(func(inputPath string, input *service.AbortMultipartUploadInput) {
		assert.Equal(t, path, inputPath)
		assert.Equal(t, client.segments[path].ID, *input.UploadID)
	})
	err := client.AbortSegment(path)
	assert.NoError(t, err)
	assert.Equal(t, 0, len(client.segments))

	// Test not exist segment.
	path = uuid.New().String()
	err = client.AbortSegment(path)
	assert.Error(t, err)
	assert.True(t, errors.Is(err, segment.ErrSegmentNotInitiated))
}

func TestClient_CreateDir(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockService := NewMockService(ctrl)

	client := Client{
		service: mockService,
	}

	// Test case1: without location
	path := uuid.New().String()
	err := client.CreateDir(path)
	assert.Error(t, err)
	assert.True(t, errors.Is(err, types.ErrPairRequired))

	// Test case2: with valid location.
	path = uuid.New().String()
	location := uuid.New().String()

	// Monkey the bucket's Put method
	bucket := &service.Bucket{}
	fn := func(*service.Bucket) (*service.PutBucketOutput, error) {
		t.Log("Bucket put has been called")
		return &service.PutBucketOutput{}, nil
	}
	monkey.PatchInstanceMethod(reflect.TypeOf(bucket), "Put", fn)

	mockService.EXPECT().Bucket(gomock.Any(), gomock.Any()).Do(func(inputPath, inputLocation string) {
		assert.Equal(t, path, inputPath)
		assert.Equal(t, location, inputLocation)
	}).Return(bucket, nil)

	err = client.CreateDir(path, types.WithLocation(location))
	assert.NoError(t, err)
}

func TestClient_CompleteSegment(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockBucket := NewMockBucket(ctrl)

	tests := []struct {
		name     string
		path     string
		segments map[string]*segment.Segment
		hasCall  bool
		mockFn   func(string, *service.CompleteMultipartUploadInput)
		hasError bool
		wantErr  error
	}{
		{
			"not initiated segment",
			"", map[string]*segment.Segment{},
			false, nil, true,
			segment.ErrSegmentNotInitiated,
		},
		{
			"segment part empty",
			"test",
			map[string]*segment.Segment{
				"test": {
					ID:    "test_segment_id",
					Parts: nil,
				},
			},

			false, nil,
			true, segment.ErrSegmentPartsEmpty,
		},
		{
			"valid segment",
			"test",
			map[string]*segment.Segment{
				"test": {
					ID: "test_segment_id",
					Parts: []*segment.Part{
						{Offset: 0, Size: 1},
					},
				},
			},
			true,
			func(inputPath string, input *service.CompleteMultipartUploadInput) {
				assert.Equal(t, "test", inputPath)
				assert.Equal(t, "test_segment_id", *input.UploadID)
			},
			false, nil,
		},
	}

	for _, v := range tests {
		if v.hasCall {
			mockBucket.EXPECT().CompleteMultipartUpload(gomock.Any(), gomock.Any()).Do(v.mockFn)
		}

		client := Client{
			bucket:   mockBucket,
			segments: v.segments,
		}

		err := client.CompleteSegment(v.path)
		if v.hasError {
			assert.Error(t, err)
			assert.True(t, errors.Is(err, v.wantErr))
		} else {
			assert.NoError(t, err)
			assert.Equal(t, 0, len(client.segments))
		}
	}
}

func TestClient_Copy(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockBucket := NewMockBucket(ctrl)

	tests := []struct {
		name     string
		src      string
		dst      string
		mockFn   func(string, *service.PutObjectInput)
		hasError bool
		wantErr  error
	}{
		{
			"valid copy",
			"test_src", "test_dst",
			func(inputObjectKey string, input *service.PutObjectInput) {
				assert.Equal(t, "test_dst", inputObjectKey)
				assert.Equal(t, "test_src", *input.XQSCopySource)
			},
			false, nil,
		},
	}

	for _, v := range tests {
		mockBucket.EXPECT().PutObject(gomock.Any(), gomock.Any()).Do(v.mockFn)

		client := Client{
			bucket: mockBucket,
		}

		err := client.Copy(v.src, v.dst)
		if v.hasError {
			assert.Error(t, err)
			assert.True(t, errors.Is(err, v.wantErr))
		} else {
			assert.NoError(t, err)
		}
	}
}

func TestClient_Delete(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockBucket := NewMockBucket(ctrl)

	tests := []struct {
		name     string
		src      string
		mockFn   func(string)
		hasError bool
		wantErr  error
	}{
		{
			"valid delete",
			"test_src",
			func(inputObjectKey string) {
				assert.Equal(t, "test_src", inputObjectKey)
			},
			false, nil,
		},
	}

	for _, v := range tests {
		mockBucket.EXPECT().DeleteObject(gomock.Any()).Do(v.mockFn)

		client := Client{
			bucket: mockBucket,
		}

		err := client.Delete(v.src)
		if v.hasError {
			assert.Error(t, err)
			assert.True(t, errors.Is(err, v.wantErr))
		} else {
			assert.NoError(t, err)
		}
	}
}

func TestClient_InitSegment(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockBucket := NewMockBucket(ctrl)

	tests := []struct {
		name     string
		path     string
		segments map[string]*segment.Segment
		hasCall  bool
		mockFn   func(string, *service.InitiateMultipartUploadInput) (*service.InitiateMultipartUploadOutput, error)
		hasError bool
		wantErr  error
	}{
		{
			"valid init segment",
			"test", map[string]*segment.Segment{},
			true,
			func(inputPath string, input *service.InitiateMultipartUploadInput) (*service.InitiateMultipartUploadOutput, error) {
				assert.Equal(t, "test", inputPath)

				uploadID := "test"
				return &service.InitiateMultipartUploadOutput{
					UploadID: &uploadID,
				}, nil
			},
			false, nil,
		},
		{
			"segment already exist",
			"test",
			map[string]*segment.Segment{
				"test": {
					ID:    "test_segment_id",
					Parts: nil,
				},
			},
			false, nil,
			true, segment.ErrSegmentAlreadyInitiated,
		},
	}

	for _, v := range tests {
		if v.hasCall {
			mockBucket.EXPECT().InitiateMultipartUpload(gomock.Any(), gomock.Any()).DoAndReturn(v.mockFn)
		}

		client := Client{
			bucket:   mockBucket,
			segments: v.segments,
		}

		err := client.InitSegment(v.path)
		if v.hasError {
			assert.Error(t, err)
			assert.True(t, errors.Is(err, v.wantErr))
		} else {
			assert.NoError(t, err)
		}
	}
}

func TestClient_ListDir(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockBucket := NewMockBucket(ctrl)

	tests := []struct {
		name     string
		path     string
		mockFn   func(*service.ListObjectsInput) (*service.ListObjectsOutput, error)
		hasError bool
		hasDone  bool
		wantErr  error
	}{
		{
			"valid copy",
			"/test_src",
			func(input *service.ListObjectsInput) (*service.ListObjectsOutput, error) {
				assert.Equal(t, "/test_src", *input.Prefix)
				assert.Equal(t, 200, *input.Limit)
				assert.Equal(t, "", *input.Marker)
				assert.Nil(t, input.Delimiter)
				return &service.ListObjectsOutput{
					HasMore: service.Bool(false),
					Keys: []*service.KeyType{
						{Key: convert.String("test_key")},
					},
				}, nil
			},
			false, true, nil,
		},
	}

	for _, v := range tests {
		mockBucket.EXPECT().ListObjects(gomock.Any()).DoAndReturn(v.mockFn)

		client := Client{
			bucket: mockBucket,
		}

		x := client.ListDir(v.path)
		item, err := x.Next()
		if v.hasError {
			assert.Error(t, err)
			assert.True(t, errors.Is(err, v.wantErr))
		} else if v.hasDone {
			assert.NotNil(t, item)
			assert.NoError(t, err)
		}
	}
}

func TestClient_Move(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockBucket := NewMockBucket(ctrl)

	tests := []struct {
		name     string
		src      string
		dst      string
		mockFn   func(string, *service.PutObjectInput)
		hasError bool
		wantErr  error
	}{
		{
			"valid copy",
			"test_src", "test_dst",
			func(inputObjectKey string, input *service.PutObjectInput) {
				assert.Equal(t, "test_dst", inputObjectKey)
				assert.Equal(t, "test_src", *input.XQSMoveSource)
			},
			false, nil,
		},
	}

	for _, v := range tests {
		mockBucket.EXPECT().PutObject(gomock.Any(), gomock.Any()).Do(v.mockFn)

		client := Client{
			bucket: mockBucket,
		}

		err := client.Move(v.src, v.dst)
		if v.hasError {
			assert.Error(t, err)
			assert.True(t, errors.Is(err, v.wantErr))
		} else {
			assert.NoError(t, err)
		}
	}
}

func TestClient_Read(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockBucket := NewMockBucket(ctrl)

	tests := []struct {
		name     string
		path     string
		mockFn   func(string, *service.GetObjectInput) (*service.GetObjectOutput, error)
		hasError bool
		wantErr  error
	}{
		{
			"valid copy",
			"/test_src",
			func(inputPath string, input *service.GetObjectInput) (*service.GetObjectOutput, error) {
				assert.Equal(t, "/test_src", inputPath)
				return &service.GetObjectOutput{
					Body: ioutil.NopCloser(bytes.NewBuffer([]byte("content"))),
				}, nil
			},
			false, nil,
		},
	}

	for _, v := range tests {
		mockBucket.EXPECT().GetObject(gomock.Any(), gomock.Any()).DoAndReturn(v.mockFn)

		client := Client{
			bucket: mockBucket,
		}

		r, err := client.Read(v.path)
		if v.hasError {
			assert.Error(t, err)
			assert.Nil(t, r)
			assert.True(t, errors.Is(err, v.wantErr))
		} else {
			assert.NotNil(t, r)
			content, rerr := ioutil.ReadAll(r)
			assert.NoError(t, rerr)
			assert.Equal(t, "content", string(content))
		}
	}
}

func TestClient_ReadSegment(t *testing.T) {

}

func TestClient_Stat(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockBucket := NewMockBucket(ctrl)

	tests := []struct {
		name     string
		src      string
		mockFn   func(objectKey string, input *service.HeadObjectInput) (*service.HeadObjectOutput, error)
		hasError bool
		wantErr  error
	}{
		{
			"valid delete",
			"test_src",
			func(objectKey string, input *service.HeadObjectInput) (*service.HeadObjectOutput, error) {
				assert.Equal(t, "test_src", objectKey)
				length := int64(100)
				return &service.HeadObjectOutput{
					ContentLength:   &length,
					ContentType:     convert.String("test_content_type"),
					ETag:            convert.String("test_etag"),
					XQSStorageClass: convert.String("test_storage_class"),
				}, nil
			},
			false, nil,
		},
	}

	for _, v := range tests {
		mockBucket.EXPECT().HeadObject(gomock.Any(), gomock.Any()).DoAndReturn(v.mockFn)

		client := Client{
			bucket: mockBucket,
		}

		o, err := client.Stat(v.src)
		if v.hasError {
			assert.Error(t, err)
			assert.True(t, errors.Is(err, v.wantErr))
		} else {
			assert.NoError(t, err)
			assert.NotNil(t, o)
			assert.Equal(t, types.ObjectTypeFile, o.Type)
			size, ok := o.GetSize()
			assert.True(t, ok)
			assert.Equal(t, int64(100), size)
			contentType, ok := o.GetType()
			assert.True(t, ok)
			assert.Equal(t, "test_content_type", contentType)
			checkSum, ok := o.GetChecksum()
			assert.True(t, ok)
			assert.Equal(t, "test_etag", checkSum)
			storageClass, ok := o.GetStorageClass()
			assert.True(t, ok)
			assert.Equal(t, "test_storage_class", storageClass)
		}
	}
}

func TestClient_WriteFile(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockBucket := NewMockBucket(ctrl)

	tests := []struct {
		name     string
		path     string
		size     int64
		mockFn   func(string, *service.PutObjectInput) (*service.PutObjectOutput, error)
		hasError bool
		wantErr  error
	}{
		{
			"valid copy",
			"/test_src",
			100,
			func(inputPath string, input *service.PutObjectInput) (*service.PutObjectOutput, error) {
				assert.Equal(t, "/test_src", inputPath)
				return nil, nil
			},
			false, nil,
		},
	}

	for _, v := range tests {
		mockBucket.EXPECT().PutObject(gomock.Any(), gomock.Any()).DoAndReturn(v.mockFn)

		client := Client{
			bucket: mockBucket,
		}

		err := client.WriteFile(v.path, v.size, nil)
		if v.hasError {
			assert.Error(t, err)
			assert.True(t, errors.Is(err, v.wantErr))
		} else {
			assert.NoError(t, err)
		}
	}
}

func TestClient_WriteSegment(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockBucket := NewMockBucket(ctrl)

	tests := []struct {
		name     string
		path     string
		segments map[string]*segment.Segment
		offset   int64
		size     int64
		hasCall  bool
		mockFn   func(string, *service.UploadMultipartInput) (*service.UploadMultipartOutput, error)
		hasError bool
		wantErr  error
	}{
		{
			"not initiated segment",
			"", map[string]*segment.Segment{},
			0, 1, false, nil, true,
			segment.ErrSegmentNotInitiated,
		},
		{
			"valid segment",
			"test",
			map[string]*segment.Segment{
				"test": {
					ID:    "test_segment_id",
					Parts: []*segment.Part{},
				},
			}, 0, 1,
			true,
			func(objectKey string, input *service.UploadMultipartInput) (*service.UploadMultipartOutput, error) {
				assert.Equal(t, "test", objectKey)
				assert.Equal(t, "test_segment_id", *input.UploadID)

				return nil, nil
			},
			false, nil,
		},
	}

	for _, v := range tests {
		if v.hasCall {
			mockBucket.EXPECT().UploadMultipart(gomock.Any(), gomock.Any()).Do(v.mockFn)
		}

		client := Client{
			bucket:   mockBucket,
			segments: v.segments,
		}

		err := client.WriteSegment(v.path, v.offset, v.size, nil)
		if v.hasError {
			assert.Error(t, err)
			assert.True(t, errors.Is(err, v.wantErr))
		} else {
			assert.NoError(t, err)
		}
	}
}

func TestClient_WriteStream(t *testing.T) {
	client := Client{}

	assert.Panics(t, func() {
		client.WriteStream("", os.Stdin)
	})
}