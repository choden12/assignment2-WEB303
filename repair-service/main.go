package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	repairpb "vehicle-repair-microservices/proto/repair"
	vehiclepb "vehicle-repair-microservices/proto/vehicle"

	_ "github.com/lib/pq"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// ----------------------------------------------------
// CIRCUIT BREAKER
// ----------------------------------------------------

type circuitBreaker struct {
	mu           sync.Mutex
	failureCount int
	openUntil    time.Time
}

const (
	maxRetries          = 3
	requestTimeout      = 2 * time.Second
	initialBackoff      = 200 * time.Millisecond
	failureThreshold    = 3
	circuitOpenDuration = 10 * time.Second
)

// allowRequest checks whether a request to Vehicle Service is allowed.
func (cb *circuitBreaker) allowRequest() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	// Circuit is closed.
	if cb.openUntil.IsZero() {
		return true
	}

	// Circuit open period has expired.
	// Allow requests again.
	if time.Now().After(cb.openUntil) {
		log.Println("Circuit breaker reset: allowing Vehicle Service requests again")

		cb.failureCount = 0
		cb.openUntil = time.Time{}

		return true
	}

	return false
}

// recordSuccess resets the circuit breaker after a successful call.
func (cb *circuitBreaker) recordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failureCount = 0
	cb.openUntil = time.Time{}
}

// recordFailure records a failed attempt.
func (cb *circuitBreaker) recordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failureCount++

	log.Printf(
		"Circuit breaker failure count: %d/%d",
		cb.failureCount,
		failureThreshold,
	)

	if cb.failureCount >= failureThreshold {
		cb.openUntil = time.Now().Add(circuitOpenDuration)

		log.Printf(
			"CIRCUIT BREAKER OPEN: Vehicle Service calls blocked for %v",
			circuitOpenDuration,
		)
	}
}

// ----------------------------------------------------
// REPAIR SERVER
// ----------------------------------------------------

type repairServer struct {
	repairpb.UnimplementedRepairServiceServer

	db            *sql.DB
	vehicleClient vehiclepb.VehicleServiceClient
	breaker       *circuitBreaker
}

// ----------------------------------------------------
// VALIDATION
// ----------------------------------------------------

func validateRepairData(
	vehicleID int32,
	problemDescription string,
	repairStatus string,
	cost float64,
) (string, error) {

	if vehicleID <= 0 {
		return "", status.Error(
			codes.InvalidArgument,
			"vehicle ID must be greater than zero",
		)
	}

	if strings.TrimSpace(problemDescription) == "" {
		return "", status.Error(
			codes.InvalidArgument,
			"problem description is required",
		)
	}

	statusValue := strings.ToLower(
		strings.TrimSpace(repairStatus),
	)

	if statusValue == "" {
		return "", status.Error(
			codes.InvalidArgument,
			"repair status is required",
		)
	}

	if statusValue != "pending" &&
		statusValue != "in_progress" &&
		statusValue != "completed" {

		return "", status.Error(
			codes.InvalidArgument,
			"repair status must be pending, in_progress, or completed",
		)
	}

	if cost < 0 {
		return "", status.Error(
			codes.InvalidArgument,
			"repair cost cannot be negative",
		)
	}

	return statusValue, nil
}

// ----------------------------------------------------
// VEHICLE SERVICE RESILIENCE
// Timeout + Retry + Backoff + Circuit Breaker
// ----------------------------------------------------

func (s *repairServer) verifyVehicle(
	ctx context.Context,
	vehicleID int32,
) error {

	// ----------------------------------------------
	// CIRCUIT BREAKER CHECK
	// ----------------------------------------------

	if !s.breaker.allowRequest() {
		log.Println(
			"CIRCUIT BREAKER OPEN: Vehicle Service request rejected",
		)

		return status.Error(
			codes.Unavailable,
			"vehicle service temporarily unavailable (circuit breaker open)",
		)
	}

	backoff := initialBackoff

	// ----------------------------------------------
	// RETRY LOOP
	// ----------------------------------------------

	for attempt := 1; attempt <= maxRetries; attempt++ {

		log.Printf(
			"Vehicle Service call attempt %d/%d for vehicle ID %d",
			attempt,
			maxRetries,
			vehicleID,
		)

		// Each attempt has its own timeout.
		vehicleCtx, cancel := context.WithTimeout(
			ctx,
			requestTimeout,
		)

		_, err := s.vehicleClient.GetVehicle(
			vehicleCtx,
			&vehiclepb.GetVehicleRequest{
				Id: vehicleID,
			},
		)

		cancel()

		// ------------------------------------------
		// SUCCESS
		// ------------------------------------------

		if err == nil {
			log.Printf(
				"Vehicle Service call successful on attempt %d",
				attempt,
			)

			s.breaker.recordSuccess()

			return nil
		}

		grpcCode := status.Code(err)

		// ------------------------------------------
		// NON-RETRYABLE ERRORS
		// ------------------------------------------

		if grpcCode == codes.NotFound {
			return status.Error(
				codes.NotFound,
				"vehicle not found",
			)
		}

		if grpcCode == codes.InvalidArgument {
			return status.Error(
				codes.InvalidArgument,
				"invalid vehicle ID",
			)
		}

		// ------------------------------------------
		// RETRYABLE FAILURE
		// ------------------------------------------

		log.Printf(
			"Vehicle Service call attempt %d failed: %v",
			attempt,
			err,
		)

		s.breaker.recordFailure()

		// If the circuit breaker has just opened,
		// stop retrying immediately.
		if !s.breaker.allowRequest() {
			return status.Error(
				codes.Unavailable,
				"vehicle service temporarily unavailable (circuit breaker open)",
			)
		}

		// No need to sleep after final attempt.
		if attempt == maxRetries {
			break
		}

		log.Printf(
			"Retrying Vehicle Service after %v backoff",
			backoff,
		)

		select {
		case <-time.After(backoff):
			// Continue to next retry.
		case <-ctx.Done():
			return status.Error(
				codes.Unavailable,
				"vehicle service request cancelled",
			)
		}

		// Exponential backoff:
		// 200ms -> 400ms
		backoff *= 2
	}

	return status.Error(
		codes.Unavailable,
		"vehicle service is unavailable after retries",
	)
}

// ----------------------------------------------------
// CREATE REPAIR
// ----------------------------------------------------

func (s *repairServer) CreateRepair(
	ctx context.Context,
	req *repairpb.CreateRepairRequest,
) (*repairpb.RepairResponse, error) {

	repairStatus, err := validateRepairData(
		req.VehicleId,
		req.ProblemDescription,
		req.RepairStatus,
		req.Cost,
	)

	if err != nil {
		return nil, err
	}

	if err := s.verifyVehicle(ctx, req.VehicleId); err != nil {

		if status.Code(err) == codes.NotFound {
			return nil, status.Error(
				codes.NotFound,
				"cannot create repair: vehicle not found",
			)
		}

		return nil, err
	}

	query := `
		INSERT INTO repair_jobs
			(vehicle_id, problem_description, repair_status, cost)
		VALUES
			($1, $2, $3, $4)
		RETURNING
			job_id,
			vehicle_id,
			problem_description,
			repair_status,
			cost,
			created_at
	`

	repair := &repairpb.RepairJob{}
	var createdAt time.Time

	err = s.db.QueryRowContext(
		ctx,
		query,
		req.VehicleId,
		strings.TrimSpace(req.ProblemDescription),
		repairStatus,
		req.Cost,
	).Scan(
		&repair.JobId,
		&repair.VehicleId,
		&repair.ProblemDescription,
		&repair.RepairStatus,
		&repair.Cost,
		&createdAt,
	)

	if err != nil {
		log.Printf(
			"CreateRepair database error: %v",
			err,
		)

		return nil, status.Error(
			codes.Internal,
			"failed to create repair job",
		)
	}

	repair.CreatedAt = createdAt.Format(time.RFC3339)

	return &repairpb.RepairResponse{
		Repair: repair,
	}, nil
}

// ----------------------------------------------------
// GET REPAIR
// ----------------------------------------------------

func (s *repairServer) GetRepair(
	ctx context.Context,
	req *repairpb.GetRepairRequest,
) (*repairpb.RepairResponse, error) {

	if req.JobId <= 0 {
		return nil, status.Error(
			codes.InvalidArgument,
			"repair job ID must be greater than zero",
		)
	}

	query := `
		SELECT
			job_id,
			vehicle_id,
			problem_description,
			repair_status,
			cost,
			created_at
		FROM repair_jobs
		WHERE job_id = $1
	`

	repair := &repairpb.RepairJob{}
	var createdAt time.Time

	err := s.db.QueryRowContext(
		ctx,
		query,
		req.JobId,
	).Scan(
		&repair.JobId,
		&repair.VehicleId,
		&repair.ProblemDescription,
		&repair.RepairStatus,
		&repair.Cost,
		&createdAt,
	)

	if err == sql.ErrNoRows {
		return nil, status.Error(
			codes.NotFound,
			"repair job not found",
		)
	}

	if err != nil {
		log.Printf(
			"GetRepair database error: %v",
			err,
		)

		return nil, status.Error(
			codes.Internal,
			"failed to retrieve repair job",
		)
	}

	repair.CreatedAt = createdAt.Format(time.RFC3339)

	// Assignment requirement:
	// GetRepair contacts Vehicle Service through gRPC.
	if err := s.verifyVehicle(ctx, repair.VehicleId); err != nil {

		if status.Code(err) == codes.NotFound {
			return nil, status.Error(
				codes.NotFound,
				"vehicle associated with repair job was not found",
			)
		}

		return nil, err
	}

	return &repairpb.RepairResponse{
		Repair: repair,
	}, nil
}

// ----------------------------------------------------
// UPDATE REPAIR
// ----------------------------------------------------

func (s *repairServer) UpdateRepair(
	ctx context.Context,
	req *repairpb.UpdateRepairRequest,
) (*repairpb.RepairResponse, error) {

	if req.JobId <= 0 {
		return nil, status.Error(
			codes.InvalidArgument,
			"repair job ID must be greater than zero",
		)
	}

	repairStatus, err := validateRepairData(
		req.VehicleId,
		req.ProblemDescription,
		req.RepairStatus,
		req.Cost,
	)

	if err != nil {
		return nil, err
	}

	if err := s.verifyVehicle(ctx, req.VehicleId); err != nil {

		if status.Code(err) == codes.NotFound {
			return nil, status.Error(
				codes.NotFound,
				"cannot update repair: vehicle not found",
			)
		}

		return nil, err
	}

	query := `
		UPDATE repair_jobs
		SET
			vehicle_id = $1,
			problem_description = $2,
			repair_status = $3,
			cost = $4
		WHERE job_id = $5
		RETURNING
			job_id,
			vehicle_id,
			problem_description,
			repair_status,
			cost,
			created_at
	`

	repair := &repairpb.RepairJob{}
	var createdAt time.Time

	err = s.db.QueryRowContext(
		ctx,
		query,
		req.VehicleId,
		strings.TrimSpace(req.ProblemDescription),
		repairStatus,
		req.Cost,
		req.JobId,
	).Scan(
		&repair.JobId,
		&repair.VehicleId,
		&repair.ProblemDescription,
		&repair.RepairStatus,
		&repair.Cost,
		&createdAt,
	)

	if err == sql.ErrNoRows {
		return nil, status.Error(
			codes.NotFound,
			"repair job not found",
		)
	}

	if err != nil {
		log.Printf(
			"UpdateRepair database error: %v",
			err,
		)

		return nil, status.Error(
			codes.Internal,
			"failed to update repair job",
		)
	}

	repair.CreatedAt = createdAt.Format(time.RFC3339)

	return &repairpb.RepairResponse{
		Repair: repair,
	}, nil
}

// ----------------------------------------------------
// LIST REPAIRS
// ----------------------------------------------------

func (s *repairServer) ListRepairs(
	ctx context.Context,
	req *repairpb.ListRepairsRequest,
) (*repairpb.ListRepairsResponse, error) {

	query := `
		SELECT
			job_id,
			vehicle_id,
			problem_description,
			repair_status,
			cost,
			created_at
		FROM repair_jobs
		ORDER BY job_id
	`

	rows, err := s.db.QueryContext(ctx, query)

	if err != nil {
		log.Printf(
			"ListRepairs database error: %v",
			err,
		)

		return nil, status.Error(
			codes.Internal,
			"failed to retrieve repair jobs",
		)
	}

	defer rows.Close()

	var repairs []*repairpb.RepairJob

	for rows.Next() {

		repair := &repairpb.RepairJob{}
		var createdAt time.Time

		err := rows.Scan(
			&repair.JobId,
			&repair.VehicleId,
			&repair.ProblemDescription,
			&repair.RepairStatus,
			&repair.Cost,
			&createdAt,
		)

		if err != nil {
			log.Printf(
				"ListRepairs scan error: %v",
				err,
			)

			return nil, status.Error(
				codes.Internal,
				"failed to read repair jobs",
			)
		}

		repair.CreatedAt = createdAt.Format(
			time.RFC3339,
		)

		repairs = append(repairs, repair)
	}

	if err := rows.Err(); err != nil {
		log.Printf(
			"ListRepairs rows error: %v",
			err,
		)

		return nil, status.Error(
			codes.Internal,
			"failed to retrieve repair jobs",
		)
	}

	return &repairpb.ListRepairsResponse{
		Repairs: repairs,
	}, nil
}

// ----------------------------------------------------
// DELETE REPAIR
// ----------------------------------------------------

func (s *repairServer) DeleteRepair(
	ctx context.Context,
	req *repairpb.DeleteRepairRequest,
) (*repairpb.DeleteRepairResponse, error) {

	if req.JobId <= 0 {
		return nil, status.Error(
			codes.InvalidArgument,
			"repair job ID must be greater than zero",
		)
	}

	query := `
		DELETE FROM repair_jobs
		WHERE job_id = $1
	`

	result, err := s.db.ExecContext(
		ctx,
		query,
		req.JobId,
	)

	if err != nil {
		log.Printf(
			"DeleteRepair database error: %v",
			err,
		)

		return nil, status.Error(
			codes.Internal,
			"failed to delete repair job",
		)
	}

	rowsAffected, err := result.RowsAffected()

	if err != nil {
		log.Printf(
			"DeleteRepair RowsAffected error: %v",
			err,
		)

		return nil, status.Error(
			codes.Internal,
			"failed to confirm repair job deletion",
		)
	}

	if rowsAffected == 0 {
		return nil, status.Error(
			codes.NotFound,
			"repair job not found",
		)
	}

	return &repairpb.DeleteRepairResponse{
		Message: "repair job deleted successfully",
	}, nil
}

// ----------------------------------------------------
// MAIN
// ----------------------------------------------------

func main() {

	port := os.Getenv("GRPC_PORT")

	dbHost := os.Getenv("DB_HOST")
	dbPort := os.Getenv("DB_PORT")
	dbUser := os.Getenv("DB_USER")
	dbPassword := os.Getenv("DB_PASSWORD")
	dbName := os.Getenv("DB_NAME")

	vehicleServiceAddress := os.Getenv(
		"VEHICLE_SERVICE_ADDR",
	)

	if port == "" {
		port = "50052"
	}

	if vehicleServiceAddress == "" {
		vehicleServiceAddress = "localhost:50051"
	}

	// ------------------------------------------------
	// PostgreSQL connection
	// ------------------------------------------------

	connectionString := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		dbHost,
		dbPort,
		dbUser,
		dbPassword,
		dbName,
	)

	db, err := sql.Open(
		"postgres",
		connectionString,
	)

	if err != nil {
		log.Fatalf(
			"Failed to open Repair database: %v",
			err,
		)
	}

	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatalf(
			"Failed to connect to Repair database: %v",
			err,
		)
	}

	fmt.Println("Connected to Repair Database")

	// ------------------------------------------------
	// Vehicle Service gRPC client
	// ------------------------------------------------

	vehicleConnection, err := grpc.NewClient(
		vehicleServiceAddress,
		grpc.WithTransportCredentials(
			insecure.NewCredentials(),
		),
	)

	if err != nil {
		log.Fatalf(
			"Failed to create Vehicle Service gRPC connection: %v",
			err,
		)
	}

	defer vehicleConnection.Close()

	vehicleClient :=
		vehiclepb.NewVehicleServiceClient(
			vehicleConnection,
		)

	fmt.Println(
		"Vehicle Service gRPC client configured:",
		vehicleServiceAddress,
	)

	// ------------------------------------------------
	// Repair Service gRPC server
	// ------------------------------------------------

	listener, err := net.Listen(
		"tcp",
		":"+port,
	)

	if err != nil {
		log.Fatalf(
			"Failed to listen: %v",
			err,
		)
	}

	grpcServer := grpc.NewServer()

	server := &repairServer{
		db:            db,
		vehicleClient: vehicleClient,
		breaker:       &circuitBreaker{},
	}

	repairpb.RegisterRepairServiceServer(
		grpcServer,
		server,
	)

	fmt.Println(
		"Repair Service is running on port",
		port,
	)

	fmt.Println(
		"Resilience enabled: timeout + retry/backoff + circuit breaker",
	)

	if err := grpcServer.Serve(listener); err != nil {
		log.Fatalf(
			"Failed to serve: %v",
			err,
		)
	}
}
