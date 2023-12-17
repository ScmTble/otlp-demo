package main

import (
	"context"
	"fmt"
	pb "grpc-demo/proto"
	"log"
	"net"
	"sync"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/reflection"

	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

var resource *sdkresource.Resource
var initResourcesOnce sync.Once

func initResource() *sdkresource.Resource {
	initResourcesOnce.Do(func() {
		extraResources, _ := sdkresource.New(
			context.Background(),
			sdkresource.WithOS(),
			sdkresource.WithProcess(),
			sdkresource.WithContainer(),
			sdkresource.WithHost(),
			sdkresource.WithAttributes(semconv.ServiceNameKey.String("service-one")),
		)
		resource, _ = sdkresource.Merge(
			sdkresource.Default(),
			extraResources,
		)
	})
	return resource
}

func initTracerProvider() *sdktrace.TracerProvider {
	ctx, cancelFunc := context.WithTimeoutCause(context.Background(), time.Second*5, fmt.Errorf("init tracer provider timeout"))
	defer cancelFunc()

	exporter, err := otlptracegrpc.New(
		ctx,
		otlptracegrpc.WithInsecure(),
		otlptracegrpc.WithEndpoint("127.0.0.1:4317"),
		otlptracegrpc.WithDialOption(grpc.WithBlock()),
	)
	if err != nil {
		log.Fatalf("new otlp trace grpc exporter failed: %v", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(initResource()),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	return tp
}

type OneService struct {
	pb.UnimplementedServiceOneServer

	twoService pb.ServiceTwoClient
}

func (h *OneService) HelloOne(ctx context.Context, req *pb.HelloOneRequest) (*pb.HelloOneResponse, error) {
	_, err := h.twoService.HelloTwo(ctx, &pb.HelloTwoRequest{})
	if err != nil {
		return nil, err
	}

	return &pb.HelloOneResponse{}, nil
}

func main() {
	tp := initTracerProvider()
	defer func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			log.Printf("Error shutting down tracer provider: %v", err)
		}
	}()

	l, err := net.Listen("tcp", ":8080")
	if err != nil {
		panic(err)

	}

	ctx, cancelFunc := context.WithTimeout(context.Background(), time.Second*5)
	defer cancelFunc()
	conn, err := grpc.DialContext(
		ctx,
		"127.0.0.1:8081",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)
	if err != nil {
		panic(err)
	}

	twoServiceClient := pb.NewServiceTwoClient(conn)

	s := grpc.NewServer(
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
	)

	reflection.Register(s)
	pb.RegisterServiceOneServer(s, &OneService{
		twoService: twoServiceClient,
	})

	fmt.Println("server is running at port 8080")

	if err := s.Serve(l); err != nil {
		panic(err)
	}
}
