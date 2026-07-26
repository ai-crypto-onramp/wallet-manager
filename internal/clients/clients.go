// Package clients implements the real gRPC client wrappers for the
// downstream services wallet-management depends on:
//
//   - MPCSigningClient dials mpc-signing-service and implements grpcclient.MPCSigner.
//   - GatewayClient dials blockchain-gateway and implements grpcclient.GatewayClient.
//
// Both clients use the generated stubs in internal/pb (walletpb). They dial on
// construction and hold a single *grpc.ClientConn for the lifetime of the
// process. In production (DEV_MODE!=1) the default transport is TLS/mTLS using
// credentials loaded from TLS_CA_CERT_FILE / TLS_CLIENT_CERT_FILE /
// TLS_CLIENT_KEY_FILE; in DEV_MODE=1 the default falls back to plaintext
// insecure credentials for the local harness. Custom dial options can be
// supplied via the Options struct to override either default.
package clients

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"os"
	"sync"

	"github.com/ai-crypto-onramp/wallet-manager/internal/grpcclient"
	walletpb "github.com/ai-crypto-onramp/wallet-manager/internal/pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// DialOption is a functional option for configuring a gRPC client.
type DialOption func(*dialConfig)

type dialConfig struct {
	grpcOpts []grpc.DialOption
}

// WithGRPCDialOptions overrides the default gRPC dial options (TLS in prod,
// insecure in DEV_MODE=1). Use this to plug in interceptors, custom
// transport creds, or in-test bufconn dialers.
func WithGRPCDialOptions(opts ...grpc.DialOption) DialOption {
	return func(c *dialConfig) {
		c.grpcOpts = opts
	}
}

// defaultDialConfig returns the production TLS dial options when devMode is
// false, loading the CA and client cert/key from TLS_CA_CERT_FILE,
// TLS_CLIENT_CERT_FILE, and TLS_CLIENT_KEY_FILE. In DEV_MODE=1 (devMode true)
// it returns insecure transport credentials for the local harness.
// In production, missing or unreadable TLS material is fatal.
func defaultDialConfig(devMode bool) dialConfig {
	if devMode {
		return dialConfig{
			grpcOpts: []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())},
		}
	}
	caPath := os.Getenv("TLS_CA_CERT_FILE")
	certPath := os.Getenv("TLS_CLIENT_CERT_FILE")
	keyPath := os.Getenv("TLS_CLIENT_KEY_FILE")
	if caPath == "" || certPath == "" || keyPath == "" {
		log.Fatalf("TLS_CA_CERT_FILE, TLS_CLIENT_CERT_FILE, and TLS_CLIENT_KEY_FILE required in production mode — set DEV_MODE=1 for local dev")
	}
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		log.Fatalf("read TLS_CA_CERT_FILE %q: %v", caPath, err)
	}
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caPEM) {
		log.Fatalf("TLS_CA_CERT_FILE %q: no PEM certificates parsed", caPath)
	}
	clientCert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		log.Fatalf("load TLS client cert/key (%q, %q): %v", certPath, keyPath, err)
	}
	tlsCfg := &tls.Config{
		RootCAs:      caPool,
		Certificates: []tls.Certificate{clientCert},
		MinVersion:   tls.VersionTLS12,
	}
	return dialConfig{
		grpcOpts: []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg))},
	}
}

// MPCSigningClient is a real gRPC client for mpc-signing-service. It satisfies
// grpcclient.MPCSigner.
type MPCSigningClient struct {
	conn *grpc.ClientConn
	raw  walletpb.MPCSigningServiceClient
	mu   sync.Mutex
}

// NewMPCSigningClient dials mpc-signing-service at target (e.g.
// "dns:///localhost:9091") and returns a client implementing grpcclient.MPCSigner.
// When devMode is false the connection uses mTLS with material from
// TLS_CA_CERT_FILE / TLS_CLIENT_CERT_FILE / TLS_CLIENT_KEY_FILE; when devMode
// is true it falls back to insecure plaintext for the local harness.
func NewMPCSigningClient(target string, devMode bool, opts ...DialOption) (*MPCSigningClient, error) {
	if target == "" {
		return nil, fmt.Errorf("dial mpc-signing-service: empty target")
	}
	cfg := defaultDialConfig(devMode)
	for _, o := range opts {
		o(&cfg)
	}
	conn, err := grpc.NewClient(target, cfg.grpcOpts...)
	if err != nil {
		return nil, fmt.Errorf("dial mpc-signing-service %q: %w", target, err)
	}
	return &MPCSigningClient{conn: conn, raw: walletpb.NewMPCSigningServiceClient(conn)}, nil
}

// Sign calls MPCSigningService.Sign over gRPC.
func (c *MPCSigningClient) Sign(ctx context.Context, req *grpcclient.SignRequest) (*grpcclient.SignResponse, error) {
	resp, err := c.raw.Sign(ctx, &walletpb.SignRequest{
		KeyId:    req.KeyID,
		TxBytes:  req.TxBytes,
		WalletId: req.WalletID.String(),
	})
	if err != nil {
		return nil, err
	}
	return &grpcclient.SignResponse{Signature: resp.Signature, SignerID: resp.SignerId}, nil
}

// Close releases the underlying gRPC connection. Safe to call multiple times.
func (c *MPCSigningClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		err := c.conn.Close()
		c.conn = nil
		return err
	}
	return nil
}

// GatewayClient is a real gRPC client for blockchain-gateway. It satisfies
// grpcclient.GatewayClient.
type GatewayClient struct {
	conn *grpc.ClientConn
	raw  walletpb.GatewayServiceClient
	mu   sync.Mutex
}

// NewGatewayClient dials blockchain-gateway at target (e.g.
// "dns:///localhost:9092") and returns a client implementing grpcclient.GatewayClient.
// When devMode is false the connection uses mTLS with material from
// TLS_CA_CERT_FILE / TLS_CLIENT_CERT_FILE / TLS_CLIENT_KEY_FILE; when devMode
// is true it falls back to insecure plaintext for the local harness.
func NewGatewayClient(target string, devMode bool, opts ...DialOption) (*GatewayClient, error) {
	if target == "" {
		return nil, fmt.Errorf("dial blockchain-gateway: empty target")
	}
	cfg := defaultDialConfig(devMode)
	for _, o := range opts {
		o(&cfg)
	}
	conn, err := grpc.NewClient(target, cfg.grpcOpts...)
	if err != nil {
		return nil, fmt.Errorf("dial blockchain-gateway %q: %w", target, err)
	}
	return &GatewayClient{conn: conn, raw: walletpb.NewGatewayServiceClient(conn)}, nil
}

// BroadcastTx calls GatewayService.BroadcastTx over gRPC.
func (c *GatewayClient) BroadcastTx(ctx context.Context, req *grpcclient.BroadcastRequest) (*grpcclient.BroadcastResponse, error) {
	resp, err := c.raw.BroadcastTx(ctx, &walletpb.BroadcastTxRequest{
		Chain:    req.Chain,
		TxBytes:  req.TxBytes,
		WalletId: req.WalletID.String(),
	})
	if err != nil {
		return nil, err
	}
	return &grpcclient.BroadcastResponse{TxHash: resp.TxHash}, nil
}

// Close releases the underlying gRPC connection. Safe to call multiple times.
func (c *GatewayClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		err := c.conn.Close()
		c.conn = nil
		return err
	}
	return nil
}

// Compile-time interface conformance checks.
var (
	_ grpcclient.MPCSigner     = (*MPCSigningClient)(nil)
	_ grpcclient.GatewayClient = (*GatewayClient)(nil)
)
