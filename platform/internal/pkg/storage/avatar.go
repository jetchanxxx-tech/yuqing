// Package storage implements confined object adapters for application ports.
package storage

const (
	AvatarMaxBytes     = 2 << 20
	AvatarMaxDimension = 4096
	AvatarURLPrefix    = "/api/v1/avatars/"
)
