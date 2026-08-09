package authtoken

import (
	"context"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// grpcSkipMethods bypass auth (reflection + health).
var grpcSkipMethods = map[string]bool{
	"/grpc.health.v1.Health/Check":                  true,
	"/grpc.reflection.v1alpha.ServerReflectionInfo": true,
	"/grpc.reflection.v1.ServerReflectionInfo":      true,
}

// GRPCUnaryInterceptor returns a grpc.UnaryServerInterceptor that validates
// the HS256 Bearer token from the "authorization" gRPC metadata entry. When
// bypass is true (DEV_MODE with no secret) it is a no-op.
func GRPCUnaryInterceptor(secret string, bypass bool) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if bypass || grpcSkipMethods[info.FullMethod] {
			return handler(ctx, req)
		}
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "missing metadata")
		}
		var token string
		if vs := md.Get("authorization"); len(vs) > 0 {
			token = strings.TrimPrefix(vs[0], "Bearer ")
		}
		if token == "" {
			if vs := md.Get("x-service-token"); len(vs) > 0 {
				token = vs[0]
			}
		}
		if token == "" {
			return nil, status.Error(codes.Unauthenticated, "missing or malformed Authorization metadata")
		}
		claims, err := verify(token, secret)
		if err != nil {
			return nil, status.Error(codes.Unauthenticated, err.Error())
		}
		if time.Now().Unix() > claims.Exp {
			return nil, status.Error(codes.Unauthenticated, "token expired")
		}
		return handler(ctx, req)
	}
}