//go:build !windows

package servicehost

import "context"

func Run(name string, f func(context.Context) error) error { return f(context.Background()) }
