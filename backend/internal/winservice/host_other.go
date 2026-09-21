//go:build !windows

package winservice

import "context"

func Run(_ string, _ func(context.Context) error) (bool, error) { return false, nil }
func IsServiceContext(context.Context) bool                     { return false }
