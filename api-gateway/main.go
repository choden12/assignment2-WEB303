package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	repairpb "vehicle-repair-microservices/proto/repair"
	vehiclepb "vehicle-repair-microservices/proto/vehicle"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// gateway contains the gRPC clients used by the REST API Gateway.
type gateway struct {
	vehicleClient vehiclepb.VehicleServiceClient
	repairClient  repairpb.RepairServiceClient
}

// errorResponse is used for consistent JSON error responses.
type errorResponse struct {
	Error string `json:"error"`
}

// writeJSON sends a JSON response to the REST client.
func writeJSON(w http.ResponseWriter, httpStatus int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)

	if data != nil {
		if err := json.NewEncoder(w).Encode(data); err != nil {
			log.Printf("Failed to encode JSON response: %v", err)
		}
	}
}

// writeGRPCError converts gRPC status codes into appropriate HTTP status codes.
func writeGRPCError(w http.ResponseWriter, err error) {
	grpcStatus, ok := status.FromError(err)

	if !ok {
		writeJSON(
			w,
			http.StatusInternalServerError,
			errorResponse{Error: "internal server error"},
		)
		return
	}

	httpStatus := http.StatusInternalServerError

	switch grpcStatus.Code() {
	case codes.InvalidArgument:
		httpStatus = http.StatusBadRequest

	case codes.NotFound:
		httpStatus = http.StatusNotFound

	case codes.AlreadyExists:
		httpStatus = http.StatusConflict

	case codes.Unavailable:
		httpStatus = http.StatusServiceUnavailable

	case codes.DeadlineExceeded:
		httpStatus = http.StatusGatewayTimeout

	case codes.Internal:
		httpStatus = http.StatusInternalServerError
	}

	writeJSON(
		w,
		httpStatus,
		errorResponse{
			Error: grpcStatus.Message(),
		},
	)
}

// requestContext gives each REST -> gRPC call a timeout.
func requestContext(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), 5*time.Second)
}

// ----------------------------------------------------
// VEHICLE HANDLERS
// ----------------------------------------------------

// createVehicle handles:
// POST /vehicles
func (g *gateway) createVehicle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RegistrationNumber string `json:"registration_number"`
		OwnerName          string `json:"owner_name"`
		Model              string `json:"model"`
		Year               int32  `json:"year"`
	}

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&req); err != nil {
		writeJSON(
			w,
			http.StatusBadRequest,
			errorResponse{Error: "invalid JSON request"},
		)
		return
	}

	ctx, cancel := requestContext(r)
	defer cancel()

	response, err := g.vehicleClient.CreateVehicle(
		ctx,
		&vehiclepb.CreateVehicleRequest{
			RegistrationNumber: req.RegistrationNumber,
			OwnerName:          req.OwnerName,
			Model:              req.Model,
			Year:               req.Year,
		},
	)

	if err != nil {
		writeGRPCError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, response)
}

// getVehicle handles:
// GET /vehicles/{id}
func (g *gateway) getVehicle(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))

	if err != nil {
		writeJSON(
			w,
			http.StatusBadRequest,
			errorResponse{Error: "vehicle ID must be a positive integer"},
		)
		return
	}

	ctx, cancel := requestContext(r)
	defer cancel()

	response, err := g.vehicleClient.GetVehicle(
		ctx,
		&vehiclepb.GetVehicleRequest{
			Id: id,
		},
	)

	if err != nil {
		writeGRPCError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, response)
}

// updateVehicle handles:
// PUT /vehicles/{id}
func (g *gateway) updateVehicle(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))

	if err != nil {
		writeJSON(
			w,
			http.StatusBadRequest,
			errorResponse{Error: "vehicle ID must be a positive integer"},
		)
		return
	}

	var req struct {
		RegistrationNumber string `json:"registration_number"`
		OwnerName          string `json:"owner_name"`
		Model              string `json:"model"`
		Year               int32  `json:"year"`
	}

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&req); err != nil {
		writeJSON(
			w,
			http.StatusBadRequest,
			errorResponse{Error: "invalid JSON request"},
		)
		return
	}

	ctx, cancel := requestContext(r)
	defer cancel()

	response, err := g.vehicleClient.UpdateVehicle(
		ctx,
		&vehiclepb.UpdateVehicleRequest{
			Id:                 id,
			RegistrationNumber: req.RegistrationNumber,
			OwnerName:          req.OwnerName,
			Model:              req.Model,
			Year:               req.Year,
		},
	)

	if err != nil {
		writeGRPCError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, response)
}

// deleteVehicle handles:
// DELETE /vehicles/{id}
func (g *gateway) deleteVehicle(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))

	if err != nil {
		writeJSON(
			w,
			http.StatusBadRequest,
			errorResponse{Error: "vehicle ID must be a positive integer"},
		)
		return
	}

	ctx, cancel := requestContext(r)
	defer cancel()

	response, err := g.vehicleClient.DeleteVehicle(
		ctx,
		&vehiclepb.DeleteVehicleRequest{
			Id: id,
		},
	)

	if err != nil {
		writeGRPCError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, response)
}

// listVehicles handles:
// GET /vehicles
func (g *gateway) listVehicles(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := requestContext(r)
	defer cancel()

	response, err := g.vehicleClient.ListVehicles(
		ctx,
		&vehiclepb.ListVehiclesRequest{},
	)

	if err != nil {
		writeGRPCError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, response)
}

// ----------------------------------------------------
// REPAIR HANDLERS
// ----------------------------------------------------

// createRepair handles:
// POST /repairs
func (g *gateway) createRepair(w http.ResponseWriter, r *http.Request) {
	var req struct {
		VehicleID          int32   `json:"vehicle_id"`
		ProblemDescription string  `json:"problem_description"`
		RepairStatus       string  `json:"repair_status"`
		Cost               float64 `json:"cost"`
	}

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&req); err != nil {
		writeJSON(
			w,
			http.StatusBadRequest,
			errorResponse{Error: "invalid JSON request"},
		)
		return
	}

	ctx, cancel := requestContext(r)
	defer cancel()

	response, err := g.repairClient.CreateRepair(
		ctx,
		&repairpb.CreateRepairRequest{
			VehicleId:          req.VehicleID,
			ProblemDescription: req.ProblemDescription,
			RepairStatus:       req.RepairStatus,
			Cost:               req.Cost,
		},
	)

	if err != nil {
		writeGRPCError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, response)
}

// getRepair handles:
// GET /repairs/{id}
func (g *gateway) getRepair(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))

	if err != nil {
		writeJSON(
			w,
			http.StatusBadRequest,
			errorResponse{Error: "repair job ID must be a positive integer"},
		)
		return
	}

	ctx, cancel := requestContext(r)
	defer cancel()

	response, err := g.repairClient.GetRepair(
		ctx,
		&repairpb.GetRepairRequest{
			JobId: id,
		},
	)

	if err != nil {
		writeGRPCError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, response)
}

// updateRepair handles:
// PUT /repairs/{id}
func (g *gateway) updateRepair(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))

	if err != nil {
		writeJSON(
			w,
			http.StatusBadRequest,
			errorResponse{Error: "repair job ID must be a positive integer"},
		)
		return
	}

	var req struct {
		VehicleID          int32   `json:"vehicle_id"`
		ProblemDescription string  `json:"problem_description"`
		RepairStatus       string  `json:"repair_status"`
		Cost               float64 `json:"cost"`
	}

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&req); err != nil {
		writeJSON(
			w,
			http.StatusBadRequest,
			errorResponse{Error: "invalid JSON request"},
		)
		return
	}

	ctx, cancel := requestContext(r)
	defer cancel()

	response, err := g.repairClient.UpdateRepair(
		ctx,
		&repairpb.UpdateRepairRequest{
			JobId:              id,
			VehicleId:          req.VehicleID,
			ProblemDescription: req.ProblemDescription,
			RepairStatus:       req.RepairStatus,
			Cost:               req.Cost,
		},
	)

	if err != nil {
		writeGRPCError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, response)
}

// deleteRepair handles:
// DELETE /repairs/{id}
func (g *gateway) deleteRepair(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))

	if err != nil {
		writeJSON(
			w,
			http.StatusBadRequest,
			errorResponse{Error: "repair job ID must be a positive integer"},
		)
		return
	}

	ctx, cancel := requestContext(r)
	defer cancel()

	response, err := g.repairClient.DeleteRepair(
		ctx,
		&repairpb.DeleteRepairRequest{
			JobId: id,
		},
	)

	if err != nil {
		writeGRPCError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, response)
}

// listRepairs handles:
// GET /repairs
func (g *gateway) listRepairs(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := requestContext(r)
	defer cancel()

	response, err := g.repairClient.ListRepairs(
		ctx,
		&repairpb.ListRepairsRequest{},
	)

	if err != nil {
		writeGRPCError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, response)
}

// ----------------------------------------------------
// HELPERS
// ----------------------------------------------------

func parseID(value string) (int32, error) {
	id, err := strconv.ParseInt(value, 10, 32)

	if err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid ID")
	}

	return int32(id), nil
}

// ----------------------------------------------------
// MAIN
// ----------------------------------------------------

func main() {
	httpPort := os.Getenv("HTTP_PORT")
	vehicleServiceAddress := os.Getenv("VEHICLE_SERVICE_ADDR")
	repairServiceAddress := os.Getenv("REPAIR_SERVICE_ADDR")

	if httpPort == "" {
		httpPort = "8080"
	}

	if vehicleServiceAddress == "" {
		vehicleServiceAddress = "localhost:50051"
	}

	if repairServiceAddress == "" {
		repairServiceAddress = "localhost:50052"
	}

	// ------------------------------------------------
	// Vehicle Service gRPC connection
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

	// ------------------------------------------------
	// Repair Service gRPC connection
	// ------------------------------------------------

	repairConnection, err := grpc.NewClient(
		repairServiceAddress,
		grpc.WithTransportCredentials(
			insecure.NewCredentials(),
		),
	)

	if err != nil {
		log.Fatalf(
			"Failed to create Repair Service gRPC connection: %v",
			err,
		)
	}

	defer repairConnection.Close()

	repairClient :=
		repairpb.NewRepairServiceClient(
			repairConnection,
		)

	g := &gateway{
		vehicleClient: vehicleClient,
		repairClient:  repairClient,
	}

	// ------------------------------------------------
	// REST routes
	// ------------------------------------------------

	mux := http.NewServeMux()

	mux.HandleFunc("POST /vehicles", g.createVehicle)
	mux.HandleFunc("GET /vehicles", g.listVehicles)
	mux.HandleFunc("GET /vehicles/{id}", g.getVehicle)
	mux.HandleFunc("PUT /vehicles/{id}", g.updateVehicle)
	mux.HandleFunc("DELETE /vehicles/{id}", g.deleteVehicle)

	mux.HandleFunc("POST /repairs", g.createRepair)
	mux.HandleFunc("GET /repairs", g.listRepairs)
	mux.HandleFunc("GET /repairs/{id}", g.getRepair)
	mux.HandleFunc("PUT /repairs/{id}", g.updateRepair)
	mux.HandleFunc("DELETE /repairs/{id}", g.deleteRepair)

	address := ":" + httpPort

	fmt.Println("API Gateway is running on port", httpPort)
	fmt.Println(
		"Vehicle Service gRPC address:",
		vehicleServiceAddress,
	)
	fmt.Println(
		"Repair Service gRPC address:",
		repairServiceAddress,
	)

	if err := http.ListenAndServe(address, mux); err != nil {
		log.Fatalf(
			"API Gateway failed: %v",
			err,
		)
	}
}
