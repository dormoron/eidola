package main

import (
	"context"
	"fmt"
	"log"
	"net"

	"github.com/dormoron/eidola/internal/batch"
	"github.com/dormoron/eidola/internal/circuitbreaker"
	"github.com/dormoron/eidola/internal/errs"
	"github.com/dormoron/eidola/internal/mempool"
	"github.com/dormoron/eidola/internal/security"
	"github.com/dormoron/eidola/observability/metrics"
	"github.com/dormoron/eidola/ratelimit"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	// 假设这是生成的proto代码
)

// ExampleRequest 请求示例结构
type ExampleRequest struct {
	Id             string `json:"id"`
	SensitiveInfo  string `json:"sensitive_info" encrypt:"true"`
	UserCredential string `json:"user_credential" encrypt:"mask"`
	Data           []byte `json:"data"`
}

// ExampleResponse 响应示例结构
type ExampleResponse struct {
	Id      string `json:"id"`
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// 模拟服务，使用各种优化特性
type ExampleService struct {
	// 自适应限流器
	limiter *ratelimit.AdaptiveLimiter
	// 自适应断路器
	breaker *circuitbreaker.AdaptiveCircuitBreaker
	// 批处理器
	batcher *batch.AdaptiveBatcher
	// 消息对象池
	msgPool *mempool.TypedMessagePool[*ExampleRequest]
	// 字段加密器
	encryptor security.FieldEncryptor
	// 方法级别的指标收集器
	metrics *metrics.MethodMetrics
}

// NewExampleService 创建示例服务
func NewExampleService() *ExampleService {
	// 创建自适应限流器
	limiter := ratelimit.NewAdaptiveLimiter(ratelimit.DefaultAdaptiveLimiterOptions())

	// 创建自适应断路器
	breaker := circuitbreaker.NewAdaptiveCircuitBreaker("example-service",
		circuitbreaker.DefaultAdaptiveOptions())

	// 创建字段加密器
	encryptor, _ := security.NewFieldEncryptor([]byte("example-encryption-key-12345678901234"))
	// 注册要加密的消息类型和字段
	encryptor.RegisterType(&ExampleRequest{}, []string{"SensitiveInfo", "UserCredential:mask"})

	// 创建消息对象池
	msgPool := mempool.NewTypedMessagePool[*ExampleRequest](
		mempool.DefaultMessagePoolOptions())

	// 创建方法级别的指标收集器
	methodMetrics := metrics.NewMethodMetrics(metrics.DefaultMethodMetricsOptions())

	// 创建批处理器
	processor := func(ctx context.Context, batchReq interface{}) (interface{}, error) {
		// 这里实现批处理逻辑
		requests, ok := batchReq.([]interface{})
		if !ok {
			return nil, fmt.Errorf("invalid batch request type")
		}

		// 批量处理请求
		responses := make([]interface{}, len(requests))
		for i, req := range requests {
			// 处理单个请求
			exampleReq, ok := req.(*ExampleRequest)
			if !ok {
				responses[i] = status.Error(codes.InvalidArgument, "invalid request type")
				continue
			}

			// 创建响应
			responses[i] = &ExampleResponse{
				Id:      exampleReq.Id,
				Success: true,
				Message: "processed successfully",
			}
		}
		return responses, nil
	}

	batcher := batch.NewAdaptiveBatcher(processor, batch.DefaultAdaptiveBatchOptions())

	return &ExampleService{
		limiter:   limiter,
		breaker:   breaker,
		batcher:   batcher,
		msgPool:   msgPool,
		encryptor: encryptor,
		metrics:   methodMetrics,
	}
}

// Process 实现服务方法，展示各种优化特性
func (s *ExampleService) Process(ctx context.Context, req *ExampleRequest) (*ExampleResponse, error) {
	// 记录请求度量
	ctx = s.metrics.RecordRequest(ctx, "/ExampleService/Process", 0, "unary")

	// 自适应限流
	if err := s.limiter.AllowWithContext(ctx); err != nil {
		// 记录响应度量
		s.metrics.RecordResponse(ctx, "/ExampleService/Process", 0, err, "unary")
		return nil, status.Errorf(codes.ResourceExhausted, "rate limit exceeded: %v", err)
	}

	// 从对象池获取一个请求对象用于内部处理，减少GC压力
	pooledReq := s.msgPool.Get()
	defer s.msgPool.Put(pooledReq)

	// 复制请求数据
	pooledReq.Id = req.Id
	pooledReq.SensitiveInfo = req.SensitiveInfo
	pooledReq.UserCredential = req.UserCredential
	pooledReq.Data = req.Data

	// 处理敏感字段加密
	if err := s.encryptor.Decrypt(pooledReq); err != nil {
		errEnhanced := errs.FromError(err, "ENCRYPTION_ERROR", errs.SeverityError, errs.CategoryValidation)
		// 记录增强错误
		s.metrics.RecordResponse(ctx, "/ExampleService/Process", 0, err, "unary")
		return nil, status.Errorf(codes.Internal, "failed to process sensitive data: %v", errEnhanced)
	}

	// 使用断路器执行业务逻辑
	var resp *ExampleResponse
	result, err := s.breaker.Execute(func() (interface{}, error) {
		// 这里是实际的业务逻辑

		// 模拟使用批处理器处理请求
		batchResult, batchErr := s.batcher.Process(ctx, pooledReq)
		if batchErr != nil {
			return nil, batchErr
		}

		// 转换批处理结果
		batchResp, ok := batchResult.(*ExampleResponse)
		if !ok {
			return nil, status.Error(codes.Internal, "invalid batch response type")
		}

		return batchResp, nil
	})

	// 处理错误
	if err != nil {
		// 创建增强错误，包含更详细的信息
		errorCategory := errs.CategorySystem
		if status.Code(err) == codes.Unavailable {
			errorCategory = errs.CategoryNetwork
		}

		enhancedErr := errs.FromError(err, "PROCESS_ERROR", errs.SeverityError, errorCategory)
		// 记录错误
		errs.GetGlobalErrorAggregator().Record(enhancedErr)

		// 记录响应度量
		s.metrics.RecordResponse(ctx, "/ExampleService/Process", 0, err, "unary")

		return nil, status.Errorf(codes.Internal, "processing failed: %v", enhancedErr)
	}

	// 转换结果
	resp = result.(*ExampleResponse)

	// 加密敏感字段
	if err := s.encryptor.Encrypt(resp); err != nil {
		enhancedErr := errs.FromError(err, "ENCRYPTION_ERROR", errs.SeverityError, errs.CategoryValidation)
		s.metrics.RecordResponse(ctx, "/ExampleService/Process", 0, err, "unary")
		return nil, status.Errorf(codes.Internal, "failed to encrypt response: %v", enhancedErr)
	}

	// 记录响应度量
	s.metrics.RecordResponse(ctx, "/ExampleService/Process", 0, nil, "unary")

	return resp, nil
}

func main() {
	// 创建服务实例
	service := NewExampleService()

	// 创建gRPC服务器，使用优化配置
	server := grpc.NewServer(
		grpc.MaxConcurrentStreams(100),
		grpc.ChainUnaryInterceptor(
			service.metrics.UnaryServerInterceptor(),
		),
	)

	// 注册服务 - 这里只是示例，实际使用时需要注册真实的服务
	// pb.RegisterExampleServiceServer(server, service)

	// 启动服务器
	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	fmt.Println("示例服务器正在运行，端口: 50051")
	fmt.Println("此示例展示了Eidola框架的各种优化功能的集成:")
	fmt.Println("1. 自适应限流")
	fmt.Println("2. 自适应断路器")
	fmt.Println("3. 批处理请求合并")
	fmt.Println("4. 对象池内存管理")
	fmt.Println("5. 敏感字段加密")
	fmt.Println("6. 增强错误处理")
	fmt.Println("7. 方法级指标收集")

	if err := server.Serve(lis); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}
