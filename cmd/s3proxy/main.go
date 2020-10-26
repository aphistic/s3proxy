package main

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
)

const (
	sizeKiB = 1024
	sizeMiB = 1024 * sizeKiB
)

func main() {
	awsRegion := os.Getenv("AWS_REGION")
	if awsRegion == "" {
		fmt.Fprintf(os.Stderr, "AWS_REGION is not set\n")
		os.Exit(1)
	}

	port := os.Getenv("S3PROXY_PORT")
	if port == "" {
		port = "8080"
	}

	endpoint := os.Getenv("S3PROXY_S3_ENDPOINT")
	if endpoint == "" {
		endpoint = "https://s3." + awsRegion + ".amazonaws.com"
	}

	h := newHandler(
		os.Getenv("AWS_ACCESS_KEY"),
		os.Getenv("AWS_SECRET_KEY"),
		os.Getenv("S3PROXY_S3_BUCKET"),
		endpoint,
	)

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: h,
	}

	err := srv.ListenAndServe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}
}

type handler struct {
	awsAccessKey string
	awsSecretKey string
	awsBucket    string
	awsEndpoint  string
}

func newHandler(awsAccessKey, awsSecretKey, awsBucket, awsHostname string) *handler {
	return &handler{
		awsAccessKey: awsAccessKey,
		awsSecretKey: awsSecretKey,
		awsBucket:    awsBucket,
		awsEndpoint:  awsHostname,
	}
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestKey := r.URL.Path
	if requestKey == "/" {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	awsSession, err := session.NewSession(&aws.Config{
		Endpoint: aws.String(h.awsEndpoint),
	})
	if err != nil {
		fmt.Printf("Error making session: %s\n", err)
		return
	}

	client := s3.New(awsSession)

	out, err := client.GetObject(&s3.GetObjectInput{
		Bucket: aws.String(h.awsBucket),
		Key:    aws.String(r.URL.Path),
	})
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			if aerr.Code() == s3.ErrCodeNoSuchKey {
				w.WriteHeader(http.StatusNotFound)
				return
			}
		}
		fmt.Printf("Error downloading: %s\n", err)
		return
	}
	defer out.Body.Close()

	contentLength := 0

	if out.ContentType != nil {
		w.Header().Set("content-type", *out.ContentType)
	}
	if out.ContentLength != nil {
		contentLength = int(*out.ContentLength)
		w.Header().Set("content-length", strconv.Itoa(contentLength))
	}
	if out.ContentDisposition != nil {
		w.Header().Set("content-disposition", *out.ContentDisposition)
	} else {
		if contentLength >= 50*sizeMiB {
			// If the file is bigger than 50MiB and a disposition isn't provided,
			// make it an attachment to download instead of just displaying the file.
			w.Header().Set("content-disposition", "attachment")
		}
	}
	if out.LastModified != nil {
		lastModified := *out.LastModified

		w.Header().Set("last-modified", lastModified.Format(http.TimeFormat))

		ifModSinceRaw := r.Header.Get("if-modified-since")
		if ifModSinceRaw != "" {
			if ifModSince, err := time.Parse(http.TimeFormat, ifModSinceRaw); err == nil {
				if ifModSince.Equal(lastModified) || ifModSince.Before(lastModified) {
					// The file on AWS hasn't changed, so let the client know
					w.WriteHeader(http.StatusNotModified)
					return
				}
			}
		}
	}

	totalRead := 0
	totalWritten := 0

	objReader := bufio.NewReader(out.Body)
	readBuf := make([]byte, 16384)
	for {
		readN, readErr := objReader.Read(readBuf)
		if readErr != nil && readErr != io.EOF {
			fmt.Printf("Error reading: %s\n", readErr)
			return
		}
		totalRead += readN

		readWritten := 0
		for {
			writeN, err := w.Write(readBuf[readWritten:readN])
			if err != nil {
				fmt.Printf("Error writing: %s\n", err)
				return
			}
			totalWritten += writeN

			readWritten += writeN

			if readWritten >= readN {
				break
			}
		}

		if readErr == io.EOF {
			break
		}
	}
}
