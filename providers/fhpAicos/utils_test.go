package fhpAicos

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/virtual-kubelet/virtual-kubelet/trace"

	v1 "k8s.io/api/core/v1"
)

func TestBuildKeyFromNames(t *testing.T) {
	key, err := buildKeyFromNames("default", "mypod")
	assert.NoError(t, err)
	assert.Equal(t, "default-mypod", key)
}

func TestBuildKey(t *testing.T) {
	t.Run("valid pod", func(t *testing.T) {
		pod := &v1.Pod{}
		pod.Namespace = "testns"
		pod.Name = "testpod"

		key, err := buildKey(pod)
		assert.NoError(t, err)
		assert.Equal(t, "testns-testpod", key)
	})

	t.Run("missing namespace", func(t *testing.T) {
		pod := &v1.Pod{}
		pod.Name = "testpod"

		key, err := buildKey(pod)
		assert.Error(t, err)
		assert.Empty(t, key)
		assert.Contains(t, err.Error(), "namespace not found")
	})

	t.Run("missing name", func(t *testing.T) {
		pod := &v1.Pod{}
		pod.Namespace = "testns"

		key, err := buildKey(pod)
		assert.Error(t, err)
		assert.Empty(t, key)
		assert.Contains(t, err.Error(), "name not found")
	})
}

func TestAddAttributes(t *testing.T) {
	ctx := context.TODO()
	ctx, span := trace.StartSpan(ctx, "assert-test")
	t.Run("valid attribute pairs", func(t *testing.T) {
		newCtx := addAttributes(ctx, span, "key1", "value1", "key2", "value2")
		assert.NotNil(t, newCtx)
	})

	t.Run("invalid (odd number of attributes)", func(t *testing.T) {
		newCtx := addAttributes(ctx, span, "key1", "value1", "key2")
		assert.Equal(t, ctx, newCtx) // Should return unmodified context
	})
}
