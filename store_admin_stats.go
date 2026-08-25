package main

import (
	"context"
	"fmt"
)

// AdminStatsCounter provides the aggregate counts shown on the admin
// dashboard.
type AdminStatsCounter interface {
	CountActiveVehicles(ctx context.Context) (int, error)
	CountActiveTrips(ctx context.Context) (int, error)
}

// CountActiveVehicles returns the number of vehicles with active = true.
// Used by the admin dashboard.
func (s *Store) CountActiveVehicles(ctx context.Context) (int, error) {
	n, err := s.queries.CountActiveVehicles(ctx)
	if err != nil {
		return 0, fmt.Errorf("count active vehicles: %w", err)
	}
	return int(n), nil
}

// CountActiveTrips returns the number of trips with status = 'active'.
// Used by the admin dashboard.
func (s *Store) CountActiveTrips(ctx context.Context) (int, error) {
	n, err := s.queries.CountActiveTrips(ctx)
	if err != nil {
		return 0, fmt.Errorf("count active trips: %w", err)
	}
	return int(n), nil
}
