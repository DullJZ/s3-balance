package api

import (
	"encoding/xml"
	"time"
)

// S3 XML响应结构体定义

// ListBucketsResult 列出所有存储桶的响应
type ListBucketsResult struct {
	XMLName xml.Name `xml:"ListAllMyBucketsResult"`
	Xmlns   string   `xml:"xmlns,attr"`
	Owner   Owner    `xml:"Owner"`
	Buckets Buckets  `xml:"Buckets"`
}

// Owner 所有者信息
type Owner struct {
	ID          string `xml:"ID"`
	DisplayName string `xml:"DisplayName"`
}

// Buckets 存储桶列表
type Buckets struct {
	Bucket []BucketInfo `xml:"Bucket"`
}

// BucketInfo 存储桶信息
type BucketInfo struct {
	Name         string    `xml:"Name"`
	CreationDate time.Time `xml:"CreationDate"`
}

// ListBucketResult 列出存储桶内容的响应
type ListBucketResult struct {
	XMLName        xml.Name       `xml:"ListBucketResult"`
	Xmlns          string         `xml:"xmlns,attr"`
	Name           string         `xml:"Name"`
	Prefix         string         `xml:"Prefix"`
	Marker         string         `xml:"Marker"`
	MaxKeys        int            `xml:"MaxKeys"`
	Delimiter      string         `xml:"Delimiter,omitempty"`
	IsTruncated    bool           `xml:"IsTruncated"`
	Contents       []ObjectInfo   `xml:"Contents"`
	CommonPrefixes []CommonPrefix `xml:"CommonPrefixes,omitempty"`
}

// ObjectInfo 对象信息
type ObjectInfo struct {
	Key          string    `xml:"Key"`
	LastModified time.Time `xml:"LastModified"`
	ETag         string    `xml:"ETag"`
	Size         int64     `xml:"Size"`
	StorageClass string    `xml:"StorageClass"`
	Owner        Owner     `xml:"Owner"`
}

// CommonPrefix 公共前缀
type CommonPrefix struct {
	Prefix string `xml:"Prefix"`
}

// InitiateMultipartUploadResult 初始化分片上传的响应
type InitiateMultipartUploadResult struct {
	XMLName  xml.Name `xml:"InitiateMultipartUploadResult"`
	Xmlns    string   `xml:"xmlns,attr"`
	Bucket   string   `xml:"Bucket"`
	Key      string   `xml:"Key"`
	UploadID string   `xml:"UploadId"`
}

// ListMultipartUploadsResult 列出分片上传的响应
type ListMultipartUploadsResult struct {
	XMLName            xml.Name       `xml:"ListMultipartUploadsResult"`
	Xmlns              string         `xml:"xmlns,attr"`
	Bucket             string         `xml:"Bucket"`
	KeyMarker          string         `xml:"KeyMarker"`
	UploadIdMarker     string         `xml:"UploadIdMarker"`
	NextKeyMarker      string         `xml:"NextKeyMarker"`
	NextUploadIdMarker string         `xml:"NextUploadIdMarker"`
	MaxUploads         int            `xml:"MaxUploads"`
	IsTruncated        bool           `xml:"IsTruncated"`
	Uploads            []Upload       `xml:"Upload"`
	CommonPrefixes     []CommonPrefix `xml:"CommonPrefixes,omitempty"`
}

// Upload 上传信息
type Upload struct {
	Key          string    `xml:"Key"`
	UploadID     string    `xml:"UploadId"`
	Initiator    Owner     `xml:"Initiator"`
	Owner        Owner     `xml:"Owner"`
	StorageClass string    `xml:"StorageClass"`
	Initiated    time.Time `xml:"Initiated"`
}

// ListPartsResult 列出分片的响应
type ListPartsResult struct {
	XMLName              xml.Name `xml:"ListPartsResult"`
	Xmlns                string   `xml:"xmlns,attr"`
	Bucket               string   `xml:"Bucket"`
	Key                  string   `xml:"Key"`
	UploadID             string   `xml:"UploadId"`
	PartNumberMarker     int      `xml:"PartNumberMarker"`
	NextPartNumberMarker int      `xml:"NextPartNumberMarker"`
	MaxParts             int      `xml:"MaxParts"`
	IsTruncated          bool     `xml:"IsTruncated"`
	Parts                []Part   `xml:"Part"`
}

// Part 分片信息
type Part struct {
	PartNumber   int       `xml:"PartNumber"`
	LastModified time.Time `xml:"LastModified"`
	ETag         string    `xml:"ETag"`
	Size         int64     `xml:"Size"`
}

// CompleteMultipartUpload 完成分片上传的请求
type CompleteMultipartUpload struct {
	XMLName xml.Name `xml:"CompleteMultipartUpload"`
	Parts   []Part   `xml:"Part"`
}

// CompleteMultipartUploadResult 完成分片上传的响应
type CompleteMultipartUploadResult struct {
	XMLName  xml.Name `xml:"CompleteMultipartUploadResult"`
	Xmlns    string   `xml:"xmlns,attr"`
	Location string   `xml:"Location"`
	Bucket   string   `xml:"Bucket"`
	Key      string   `xml:"Key"`
	ETag     string   `xml:"ETag"`
}

// ErrorResponse S3错误响应
type ErrorResponse struct {
	XMLName   xml.Name `xml:"Error"`
	Code      string   `xml:"Code"`
	Message   string   `xml:"Message"`
	Resource  string   `xml:"Resource"`
	RequestID string   `xml:"RequestId"`
}
