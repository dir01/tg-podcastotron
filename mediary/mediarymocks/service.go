// Code generated by moq; DO NOT EDIT.
// github.com/matryer/moq

package mediarymocks

import (
	"context"
	"sync"
	"undercast-bot/mediary"
)

// Ensure, that ServiceMock does implement mediary.Service.
// If this is not the case, regenerate this file with moq.
var _ mediary.Service = &ServiceMock{}

// ServiceMock is a mock implementation of mediary.Service.
//
//	func TestSomethingThatUsesService(t *testing.T) {
//
//		// make and configure a mocked mediary.Service
//		mockedService := &ServiceMock{
//			CreateUploadJobFunc: func(ctx context.Context, params *mediary.CreateUploadJobParams) (string, error) {
//				panic("mock out the CreateUploadJob method")
//			},
//			FetchMetadataLongPollingFunc: func(ctx context.Context, mediaURL string) (*mediary.Metadata, error) {
//				panic("mock out the FetchMetadataLongPolling method")
//			},
//			IsValidURLFunc: func(ctx context.Context, mediaURL string) (bool, error) {
//				panic("mock out the IsValidURL method")
//			},
//		}
//
//		// use mockedService in code that requires mediary.Service
//		// and then make assertions.
//
//	}
type ServiceMock struct {
	// CreateUploadJobFunc mocks the CreateUploadJob method.
	CreateUploadJobFunc func(ctx context.Context, params *mediary.CreateUploadJobParams) (string, error)

	// FetchMetadataLongPollingFunc mocks the FetchMetadataLongPolling method.
	FetchMetadataLongPollingFunc func(ctx context.Context, mediaURL string) (*mediary.Metadata, error)

	// IsValidURLFunc mocks the IsValidURL method.
	IsValidURLFunc func(ctx context.Context, mediaURL string) (bool, error)

	// calls tracks calls to the methods.
	calls struct {
		// CreateUploadJob holds details about calls to the CreateUploadJob method.
		CreateUploadJob []struct {
			// Ctx is the ctx argument value.
			Ctx context.Context
			// Params is the params argument value.
			Params *mediary.CreateUploadJobParams
		}
		// FetchMetadataLongPolling holds details about calls to the FetchMetadataLongPolling method.
		FetchMetadataLongPolling []struct {
			// Ctx is the ctx argument value.
			Ctx context.Context
			// MediaURL is the mediaURL argument value.
			MediaURL string
		}
		// IsValidURL holds details about calls to the IsValidURL method.
		IsValidURL []struct {
			// Ctx is the ctx argument value.
			Ctx context.Context
			// MediaURL is the mediaURL argument value.
			MediaURL string
		}
	}
	lockCreateUploadJob          sync.RWMutex
	lockFetchMetadataLongPolling sync.RWMutex
	lockIsValidURL               sync.RWMutex
}

// CreateUploadJob calls CreateUploadJobFunc.
func (mock *ServiceMock) CreateUploadJob(ctx context.Context, params *mediary.CreateUploadJobParams) (string, error) {
	if mock.CreateUploadJobFunc == nil {
		panic("ServiceMock.CreateUploadJobFunc: method is nil but Service.CreateUploadJob was just called")
	}
	callInfo := struct {
		Ctx    context.Context
		Params *mediary.CreateUploadJobParams
	}{
		Ctx:    ctx,
		Params: params,
	}
	mock.lockCreateUploadJob.Lock()
	mock.calls.CreateUploadJob = append(mock.calls.CreateUploadJob, callInfo)
	mock.lockCreateUploadJob.Unlock()
	return mock.CreateUploadJobFunc(ctx, params)
}

// CreateUploadJobCalls gets all the calls that were made to CreateUploadJob.
// Check the length with:
//
//	len(mockedService.CreateUploadJobCalls())
func (mock *ServiceMock) CreateUploadJobCalls() []struct {
	Ctx    context.Context
	Params *mediary.CreateUploadJobParams
} {
	var calls []struct {
		Ctx    context.Context
		Params *mediary.CreateUploadJobParams
	}
	mock.lockCreateUploadJob.RLock()
	calls = mock.calls.CreateUploadJob
	mock.lockCreateUploadJob.RUnlock()
	return calls
}

// FetchMetadataLongPolling calls FetchMetadataLongPollingFunc.
func (mock *ServiceMock) FetchMetadataLongPolling(ctx context.Context, mediaURL string) (*mediary.Metadata, error) {
	if mock.FetchMetadataLongPollingFunc == nil {
		panic("ServiceMock.FetchMetadataLongPollingFunc: method is nil but Service.FetchMetadataLongPolling was just called")
	}
	callInfo := struct {
		Ctx      context.Context
		MediaURL string
	}{
		Ctx:      ctx,
		MediaURL: mediaURL,
	}
	mock.lockFetchMetadataLongPolling.Lock()
	mock.calls.FetchMetadataLongPolling = append(mock.calls.FetchMetadataLongPolling, callInfo)
	mock.lockFetchMetadataLongPolling.Unlock()
	return mock.FetchMetadataLongPollingFunc(ctx, mediaURL)
}

// FetchMetadataLongPollingCalls gets all the calls that were made to FetchMetadataLongPolling.
// Check the length with:
//
//	len(mockedService.FetchMetadataLongPollingCalls())
func (mock *ServiceMock) FetchMetadataLongPollingCalls() []struct {
	Ctx      context.Context
	MediaURL string
} {
	var calls []struct {
		Ctx      context.Context
		MediaURL string
	}
	mock.lockFetchMetadataLongPolling.RLock()
	calls = mock.calls.FetchMetadataLongPolling
	mock.lockFetchMetadataLongPolling.RUnlock()
	return calls
}

// IsValidURL calls IsValidURLFunc.
func (mock *ServiceMock) IsValidURL(ctx context.Context, mediaURL string) (bool, error) {
	if mock.IsValidURLFunc == nil {
		panic("ServiceMock.IsValidURLFunc: method is nil but Service.IsValidURL was just called")
	}
	callInfo := struct {
		Ctx      context.Context
		MediaURL string
	}{
		Ctx:      ctx,
		MediaURL: mediaURL,
	}
	mock.lockIsValidURL.Lock()
	mock.calls.IsValidURL = append(mock.calls.IsValidURL, callInfo)
	mock.lockIsValidURL.Unlock()
	return mock.IsValidURLFunc(ctx, mediaURL)
}

// IsValidURLCalls gets all the calls that were made to IsValidURL.
// Check the length with:
//
//	len(mockedService.IsValidURLCalls())
func (mock *ServiceMock) IsValidURLCalls() []struct {
	Ctx      context.Context
	MediaURL string
} {
	var calls []struct {
		Ctx      context.Context
		MediaURL string
	}
	mock.lockIsValidURL.RLock()
	calls = mock.calls.IsValidURL
	mock.lockIsValidURL.RUnlock()
	return calls
}
