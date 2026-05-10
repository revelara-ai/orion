package main

import (
	"context"
	"time"
)

func call(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_ = ctx
}
