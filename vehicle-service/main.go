package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net"
	"os"
	"strings"

	vehiclepb "vehicle-repair-microservices/proto/vehicle"

	_ "github.com/lib/pq"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type vehicleServer struct {
	vehiclepb.UnimplementedVehicleServiceServer
	db *sql.DB
}

// ----------------------------------------------------
// CREATE VEHICLE
// ----------------------------------------------------

func (s *vehicleServer) CreateVehicle(
	ctx context.Context,
	req *vehiclepb.CreateVehicleRequest,
) (*vehiclepb.VehicleResponse, error) {

	if strings.TrimSpace(req.RegistrationNumber) == "" {
		return nil, status.Error(
			codes.InvalidArgument,
			"registration number is required",
		)
	}

	if strings.TrimSpace(req.OwnerName) == "" {
		return nil, status.Error(
			codes.InvalidArgument,
			"owner name is required",
		)
	}

	if strings.TrimSpace(req.Model) == "" {
		return nil, status.Error(
			codes.InvalidArgument,
			"vehicle model is required",
		)
	}

	if req.Year < 1900 || req.Year > 2100 {
		return nil, status.Error(
			codes.InvalidArgument,
			"vehicle year must be between 1900 and 2100",
		)
	}

	query := `
		INSERT INTO vehicles
			(registration_number, owner_name, model, year)
		VALUES
			($1, $2, $3, $4)
		RETURNING id
	`

	var id int32

	err := s.db.QueryRowContext(
		ctx,
		query,
		strings.TrimSpace(req.RegistrationNumber),
		strings.TrimSpace(req.OwnerName),
		strings.TrimSpace(req.Model),
		req.Year,
	).Scan(&id)

	if err != nil {
		log.Printf("CreateVehicle database error: %v", err)

		return nil, status.Error(
			codes.Internal,
			"failed to create vehicle",
		)
	}

	vehicle := &vehiclepb.Vehicle{
		Id:                 id,
		RegistrationNumber: strings.TrimSpace(req.RegistrationNumber),
		OwnerName:          strings.TrimSpace(req.OwnerName),
		Model:              strings.TrimSpace(req.Model),
		Year:               req.Year,
	}

	return &vehiclepb.VehicleResponse{
		Vehicle: vehicle,
	}, nil
}

// ----------------------------------------------------
// GET VEHICLE
// ----------------------------------------------------

func (s *vehicleServer) GetVehicle(
	ctx context.Context,
	req *vehiclepb.GetVehicleRequest,
) (*vehiclepb.VehicleResponse, error) {

	if req.Id <= 0 {
		return nil, status.Error(
			codes.InvalidArgument,
			"vehicle ID must be greater than zero",
		)
	}

	query := `
		SELECT
			id,
			registration_number,
			owner_name,
			model,
			year
		FROM vehicles
		WHERE id = $1
	`

	vehicle := &vehiclepb.Vehicle{}

	err := s.db.QueryRowContext(
		ctx,
		query,
		req.Id,
	).Scan(
		&vehicle.Id,
		&vehicle.RegistrationNumber,
		&vehicle.OwnerName,
		&vehicle.Model,
		&vehicle.Year,
	)

	if err == sql.ErrNoRows {
		return nil, status.Error(
			codes.NotFound,
			"vehicle not found",
		)
	}

	if err != nil {
		log.Printf("GetVehicle database error: %v", err)

		return nil, status.Error(
			codes.Internal,
			"failed to retrieve vehicle",
		)
	}

	return &vehiclepb.VehicleResponse{
		Vehicle: vehicle,
	}, nil
}

// ----------------------------------------------------
// UPDATE VEHICLE
// ----------------------------------------------------

func (s *vehicleServer) UpdateVehicle(
	ctx context.Context,
	req *vehiclepb.UpdateVehicleRequest,
) (*vehiclepb.VehicleResponse, error) {

	if req.Id <= 0 {
		return nil, status.Error(
			codes.InvalidArgument,
			"vehicle ID must be greater than zero",
		)
	}

	if strings.TrimSpace(req.RegistrationNumber) == "" {
		return nil, status.Error(
			codes.InvalidArgument,
			"registration number is required",
		)
	}

	if strings.TrimSpace(req.OwnerName) == "" {
		return nil, status.Error(
			codes.InvalidArgument,
			"owner name is required",
		)
	}

	if strings.TrimSpace(req.Model) == "" {
		return nil, status.Error(
			codes.InvalidArgument,
			"vehicle model is required",
		)
	}

	if req.Year < 1900 || req.Year > 2100 {
		return nil, status.Error(
			codes.InvalidArgument,
			"vehicle year must be between 1900 and 2100",
		)
	}

	query := `
		UPDATE vehicles
		SET
			registration_number = $1,
			owner_name = $2,
			model = $3,
			year = $4
		WHERE id = $5
		RETURNING
			id,
			registration_number,
			owner_name,
			model,
			year
	`

	vehicle := &vehiclepb.Vehicle{}

	err := s.db.QueryRowContext(
		ctx,
		query,
		strings.TrimSpace(req.RegistrationNumber),
		strings.TrimSpace(req.OwnerName),
		strings.TrimSpace(req.Model),
		req.Year,
		req.Id,
	).Scan(
		&vehicle.Id,
		&vehicle.RegistrationNumber,
		&vehicle.OwnerName,
		&vehicle.Model,
		&vehicle.Year,
	)

	if err == sql.ErrNoRows {
		return nil, status.Error(
			codes.NotFound,
			"vehicle not found",
		)
	}

	if err != nil {
		log.Printf("UpdateVehicle database error: %v", err)

		return nil, status.Error(
			codes.Internal,
			"failed to update vehicle",
		)
	}

	return &vehiclepb.VehicleResponse{
		Vehicle: vehicle,
	}, nil
}

// ----------------------------------------------------
// DELETE VEHICLE
// ----------------------------------------------------

func (s *vehicleServer) DeleteVehicle(
	ctx context.Context,
	req *vehiclepb.DeleteVehicleRequest,
) (*vehiclepb.DeleteVehicleResponse, error) {

	if req.Id <= 0 {
		return nil, status.Error(
			codes.InvalidArgument,
			"vehicle ID must be greater than zero",
		)
	}

	query := `
		DELETE FROM vehicles
		WHERE id = $1
	`

	result, err := s.db.ExecContext(
		ctx,
		query,
		req.Id,
	)

	if err != nil {
		log.Printf("DeleteVehicle database error: %v", err)

		return nil, status.Error(
			codes.Internal,
			"failed to delete vehicle",
		)
	}

	rowsAffected, err := result.RowsAffected()

	if err != nil {
		log.Printf("DeleteVehicle RowsAffected error: %v", err)

		return nil, status.Error(
			codes.Internal,
			"failed to confirm vehicle deletion",
		)
	}

	if rowsAffected == 0 {
		return nil, status.Error(
			codes.NotFound,
			"vehicle not found",
		)
	}

	return &vehiclepb.DeleteVehicleResponse{
		Message: "vehicle deleted successfully",
	}, nil
}

// ----------------------------------------------------
// LIST VEHICLES
// ----------------------------------------------------

func (s *vehicleServer) ListVehicles(
	ctx context.Context,
	req *vehiclepb.ListVehiclesRequest,
) (*vehiclepb.ListVehiclesResponse, error) {

	query := `
		SELECT
			id,
			registration_number,
			owner_name,
			model,
			year
		FROM vehicles
		ORDER BY id
	`

	rows, err := s.db.QueryContext(ctx, query)

	if err != nil {
		log.Printf("ListVehicles database error: %v", err)

		return nil, status.Error(
			codes.Internal,
			"failed to retrieve vehicles",
		)
	}

	defer rows.Close()

	var vehicles []*vehiclepb.Vehicle

	for rows.Next() {
		vehicle := &vehiclepb.Vehicle{}

		err := rows.Scan(
			&vehicle.Id,
			&vehicle.RegistrationNumber,
			&vehicle.OwnerName,
			&vehicle.Model,
			&vehicle.Year,
		)

		if err != nil {
			log.Printf("ListVehicles scan error: %v", err)

			return nil, status.Error(
				codes.Internal,
				"failed to read vehicle data",
			)
		}

		vehicles = append(vehicles, vehicle)
	}

	if err := rows.Err(); err != nil {
		log.Printf("ListVehicles rows error: %v", err)

		return nil, status.Error(
			codes.Internal,
			"failed to retrieve vehicles",
		)
	}

	return &vehiclepb.ListVehiclesResponse{
		Vehicles: vehicles,
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

	if port == "" {
		port = "50051"
	}

	connectionString := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		dbHost,
		dbPort,
		dbUser,
		dbPassword,
		dbName,
	)

	db, err := sql.Open("postgres", connectionString)

	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}

	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}

	fmt.Println("Connected to Vehicle Database")

	listener, err := net.Listen("tcp", ":"+port)

	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer()

	server := &vehicleServer{
		db: db,
	}

	vehiclepb.RegisterVehicleServiceServer(
		grpcServer,
		server,
	)

	fmt.Println("Vehicle Service is running on port", port)

	if err := grpcServer.Serve(listener); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}
