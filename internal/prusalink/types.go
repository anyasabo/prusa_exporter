package prusalink

// The types have only the fields that the exporter uses.

// Version is the response of /api/version.
type Version struct {
	API    string `json:"api"`
	Server string `json:"server"`
	Text   string `json:"text"`
}

// Job is the response of /api/job.
type Job struct {
	Job struct {
		File struct {
			Name string `json:"name"`
			Path string `json:"path"`
		} `json:"file"`
	} `json:"job"`
}

// Printer is the response of /api/printer.
type Printer struct {
	Telemetry struct {
		Material string `json:"material"`
	} `json:"telemetry"`
}

// Status is the response of /api/v1/status.
type Status struct {
	Job *struct {
		ID            float64 `json:"id"`
		Progress      float64 `json:"progress"`
		TimeRemaining float64 `json:"time_remaining"`
		TimePrinting  float64 `json:"time_printing"`
	} `json:"job"`
	Printer struct {
		State        string  `json:"state"`
		TempBed      float64 `json:"temp_bed"`
		TargetBed    float64 `json:"target_bed"`
		TempNozzle   float64 `json:"temp_nozzle"`
		TargetNozzle float64 `json:"target_nozzle"`
		AxisX        float64 `json:"axis_x"`
		AxisY        float64 `json:"axis_y"`
		AxisZ        float64 `json:"axis_z"`
		Flow         float64 `json:"flow"`
		Speed        float64 `json:"speed"`
		FanHotend    float64 `json:"fan_hotend"`
		FanPrint     float64 `json:"fan_print"`
	} `json:"printer"`
}

// Info is the response of /api/v1/info.
type Info struct {
	MMU            bool    `json:"mmu"`
	Name           string  `json:"name"`
	Location       string  `json:"location"`
	NozzleDiameter float64 `json:"nozzle_diameter"`
	Serial         string  `json:"serial"`
	Hostname       string  `json:"hostname"`
}
