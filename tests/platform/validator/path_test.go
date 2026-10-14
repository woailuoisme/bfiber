package validator_test

import (
	"errors"
	"reflect"
	"testing"

	govalidator "github.com/go-playground/validator/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bfiber/internal/platform/validator"
)

// TestNormalizePath 校验索引路径改写为点号分隔。
func TestNormalizePath(t *testing.T) {
	assert.Equal(t, "users.0.email", validator.NormalizePath("users[0].email"))
	assert.Equal(t, "a.0.1.b", validator.NormalizePath("a[0][1].b"))
	assert.Equal(t, "plain", validator.NormalizePath("plain"))
}

type nestedPayload struct {
	Items []item `json:"items" validate:"dive"`
}

type item struct {
	Name string `json:"name" validate:"required"`
}

// TestFieldPath 校验从 FieldError 提取干净的标准字段路径。
func TestFieldPath(t *testing.T) {
	instance := govalidator.New()
	instance.RegisterTagNameFunc(func(field reflect.StructField) string {
		if name := field.Tag.Get("json"); name != "" {
			return name
		}
		return field.Name
	})

	err := instance.Struct(nestedPayload{Items: []item{{}}})
	require.Error(t, err)

	valErr, ok := errors.AsType[govalidator.ValidationErrors](err)
	require.True(t, ok)
	require.NotEmpty(t, valErr)

	fieldPath := validator.FieldPath(valErr[0])
	assert.Equal(t, "items.0.name", fieldPath)
}
