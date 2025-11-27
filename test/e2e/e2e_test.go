package e2e_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestE2ETest(t *testing.T) {
	var err error = nil

	require.NoError(t, err)
	assert.Equal(t, "a", "a")
}
