package server

import (
	"context"
	"crypto/tls"

	"mcp-gateway-poc/ext-proc/handlers"
	"mcp-gateway-poc/ext-proc/internal/runnable"
	tlsutil "mcp-gateway-poc/ext-proc/internal/tls"

	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/go-logr/logr"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// ExtProcServerRunner provides methods to manage an external process server.
type ExtProcServerRunner struct {
	GrpcPort      int
	SecureServing bool
	Streaming     bool
}

func NewDefaultExtProcServerRunner(port int, streaming bool) *ExtProcServerRunner {
	return &ExtProcServerRunner{
		GrpcPort:      port,
		SecureServing: true,
		Streaming:     streaming,
	}
}

// AsRunnable returns a Runnable that can be used to start the ext-proc gRPC server.
// The runnable implements LeaderElectionRunnable with leader election disabled.
func (r *ExtProcServerRunner) AsRunnable(logger logr.Logger) manager.Runnable {
	return runnable.NoLeaderElection(manager.RunnableFunc(func(ctx context.Context) error {
		var srv *grpc.Server
		if r.SecureServing {
			cert, err := tlsutil.CreateSelfSignedTLSCertificate(logger)
			if err != nil {
				logger.Error(err, "Failed to create self signed certificate")
				return err
			}
			creds := credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{cert}})
			srv = grpc.NewServer(grpc.Creds(creds))
		} else {
			srv = grpc.NewServer()
		}

		extProcPb.RegisterExternalProcessorServer(
			srv,
			handlers.NewServer(r.Streaming, nil), // nil SessionMapper for standalone ext-proc
		)

		// Forward to the gRPC runnable.
		return runnable.GRPCServer("ext-proc", srv, r.GrpcPort).Start(ctx)
	}))
}
