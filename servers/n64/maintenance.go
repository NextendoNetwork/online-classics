package main

import (
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "npln.nintendo.net/npln-practice/proto/maintenance/v1"
	"time"
)

type maintenanceServer struct {
	pb.UnimplementedMaintenanceScheduleServiceServer
}

func (s *maintenanceServer) SubscribeMaintenanceSchedules(req *pb.SubscribeMaintenanceSchedulesRequest, stream grpc.ServerStreamingServer[pb.SubscribeMaintenanceSchedulesResponse]) error {
	if _, err := authenticatedNPLNUID(stream.Context()); err != nil {
		return err
	}
	if err := stream.Send(&pb.SubscribeMaintenanceSchedulesResponse{Response: &pb.SubscribeMaintenanceSchedulesResponse_Message{Message: &pb.SubscribeMaintenanceSchedulesResponseMessage{Timestamp: timestamppb.Now()}}}); err != nil {
		return err
	}
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-stream.Context().Done():
			return nil
		case <-ticker.C:
			if err := stream.Send(&pb.SubscribeMaintenanceSchedulesResponse{Response: &pb.SubscribeMaintenanceSchedulesResponse_KeepAlive{KeepAlive: &pb.KeepAlive{}}}); err != nil {
				return err
			}
		}
	}
}
