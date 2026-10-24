//go:build !windows && !linux

package systemservice

import "context"

func platformQuery(context.Context, string) serviceState { return unknown }
