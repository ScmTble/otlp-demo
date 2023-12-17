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
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

var tracer = otel.Tracer("service-two")
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
			sdkresource.WithAttributes(semconv.ServiceNameKey.String("service-two")),
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

type TwoService struct {
	pb.UnimplementedServiceTwoServer
}

func (h *TwoService) HelloTwo(ctx context.Context, req *pb.HelloTwoRequest) (*pb.HelloTwoResponse, error) {
	h.workHard(ctx)
	time.Sleep(50 * time.Millisecond)

	return &pb.HelloTwoResponse{}, nil
}

func (s *TwoService) workHard(ctx context.Context) {
	_, span := tracer.Start(ctx, "workHard",
		trace.WithAttributes(attribute.String("extra.key", "extra.value")))
	defer span.End()

	time.Sleep(50 * time.Millisecond)
}

func main() {
	tp := initTracerProvider()
	defer func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			log.Printf("Error shutting down tracer provider: %v", err)
		}
	}()

	l, err := net.Listen("tcp", ":8081")
	if err != nil {
		panic(err)

	}

	s := grpc.NewServer(
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
	)

	reflection.Register(s)
	pb.RegisterServiceTwoServer(s, &TwoService{})

	fmt.Println("server is running at port 8080")

	if err := s.Serve(l); err != nil {
		panic(err)
	}
}
