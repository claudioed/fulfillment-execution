package http

import "time"

type createTaskRequest struct {
	Type                 string    `json:"type"`
	CPT                  time.Time `json:"cpt"`
	OrderRef             string    `json:"orderRef"`
	RequiredCapabilities []string  `json:"requiredCapabilities"`
}

type taskResponse struct {
	Id                   string     `json:"id"`
	Type                 string     `json:"type"`
	Status               string     `json:"status"`
	CPT                  time.Time  `json:"cpt"`
	OrderRef             string     `json:"orderRef"`
	RequiredCapabilities []string   `json:"requiredCapabilities"`
	LeaseStationId       *string    `json:"leaseStationId,omitempty"`
	LeaseExpiry          *time.Time `json:"leaseExpiry,omitempty"`
}

type claimNextRequest struct {
	TaskType string `json:"taskType"`
}

type renewLeaseRequest struct {
	StationId string `json:"stationId"`
}

type completeTaskRequest struct {
	StationId string `json:"stationId"`
}

type sealPackageRequest struct {
	StationId string   `json:"stationId"`
	Contents  []string `json:"contents"`
}

type packageResponse struct {
	Id              string   `json:"id"`
	OrderRef        string   `json:"orderRef"`
	Status          string   `json:"status"`
	ScannedContents []string `json:"scannedContents"`
}

type runSlamRequest struct {
	ActualWeight   float64 `json:"actualWeight"`
	ExpectedWeight float64 `json:"expectedWeight"`
}

type queueDepthResponse struct {
	TaskType string `json:"taskType"`
	Depth    int    `json:"depth"`
}

type expireLeasesResponse struct {
	Freed int `json:"freed"`
}

type registerStationRequest struct {
	StationId    string   `json:"stationId"`
	Capabilities []string `json:"capabilities"`
}

type stationResponse struct {
	Id           string   `json:"id"`
	Capabilities []string `json:"capabilities"`
	Occupied     bool     `json:"occupied"`
}

type errorResponse struct {
	Error string `json:"error"`
}
