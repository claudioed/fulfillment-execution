package http

import "time"

type createTaskRequest struct {
	Type                 string    `json:"type"`
	CPT                  time.Time `json:"cpt"`
	OrderRef             string    `json:"orderRef"`
	RequiredCapabilities []string  `json:"requiredCapabilities"`
	Fragile              bool      `json:"fragile"`
}

// validate reports the first missing required field, or "" if the request
// is well-formed enough to hand to the use case.
func (r createTaskRequest) validate() string {
	switch {
	case r.Type == "":
		return "type is required"
	case r.CPT.IsZero():
		return "cpt is required"
	case r.OrderRef == "":
		return "orderRef is required"
	case len(r.RequiredCapabilities) == 0:
		return "requiredCapabilities must contain at least one capability"
	default:
		return ""
	}
}

type taskResponse struct {
	Id                   string     `json:"id"`
	Type                 string     `json:"type"`
	Status               string     `json:"status"`
	CPT                  time.Time  `json:"cpt"`
	OrderRef             string     `json:"orderRef"`
	RequiredCapabilities []string   `json:"requiredCapabilities"`
	Fragile              bool       `json:"fragile"`
	LeaseStationId       *string    `json:"leaseStationId,omitempty"`
	LeaseExpiry          *time.Time `json:"leaseExpiry,omitempty"`
}

type claimNextRequest struct {
	TaskType string `json:"taskType"`
}

func (r claimNextRequest) validate() string {
	if r.TaskType == "" {
		return "taskType is required"
	}
	return ""
}

type renewLeaseRequest struct {
	StationId string `json:"stationId"`
}

func (r renewLeaseRequest) validate() string {
	if r.StationId == "" {
		return "stationId is required"
	}
	return ""
}

type completeTaskRequest struct {
	StationId string `json:"stationId"`
}

func (r completeTaskRequest) validate() string {
	if r.StationId == "" {
		return "stationId is required"
	}
	return ""
}

type sealPackageRequest struct {
	StationId string   `json:"stationId"`
	Contents  []string `json:"contents"`
}

// validate checks only that stationId (the claim owner) is present; an
// empty contents slice is a domain-level concern (pack.ErrNoScannedContents,
// mapped to 422), not a malformed request.
func (r sealPackageRequest) validate() string {
	if r.StationId == "" {
		return "stationId is required"
	}
	return ""
}

type packageResponse struct {
	Id              string   `json:"id"`
	OrderRef        string   `json:"orderRef"`
	Status          string   `json:"status"`
	ScannedContents []string `json:"scannedContents"`
	FragileHandling bool     `json:"fragileHandling"`
	SortLane        string   `json:"sortLane"`
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

func (r registerStationRequest) validate() string {
	switch {
	case r.StationId == "":
		return "stationId is required"
	case len(r.Capabilities) == 0:
		return "capabilities must contain at least one capability"
	default:
		return ""
	}
}

type stationResponse struct {
	Id           string   `json:"id"`
	Capabilities []string `json:"capabilities"`
	Occupied     bool     `json:"occupied"`
}
