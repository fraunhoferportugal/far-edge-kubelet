package integration_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIntegrationTest(t *testing.T) {
	var err error = nil

	require.NoError(t, err)
	assert.Equal(t, "a", "a")
}
