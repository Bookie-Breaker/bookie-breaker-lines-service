package model

import "time"

type Sportsbook struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Key       string    `json:"key"`
	IsSharp   bool      `json:"is_sharp"`
	IsActive  bool      `json:"is_active"`
	CreatedAt time.Time `json:"created_at,omitzero"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`
}
