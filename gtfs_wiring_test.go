package main

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewGTFSRuntime_LoadsIndex(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt, err := newGTFSRuntime(ctx, "rider/testdata/fixture.zip", time.Hour)
	require.NoError(t, err)
	defer rt.Stop()
	assert.Equal(t, 3, rt.Index().Stats().Trips)
	assert.Equal(t, 2, rt.Index().Stats().Routes)
	assert.Same(t, rt.Index(), rt.Refresher().Current())
}

func TestNewGTFSRuntime_FailsWhenTheFeedIsMissing(t *testing.T) {
	_, err := newGTFSRuntime(context.Background(), "does/not/exist.zip", time.Hour)
	assert.Error(t, err)
}

func TestNewGTFSRuntime_StopReturns(t *testing.T) {
	rt, err := newGTFSRuntime(context.Background(), "rider/testdata/fixture.zip", time.Hour)
	require.NoError(t, err)
	done := make(chan struct{})
	go func() { rt.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return")
	}
}
