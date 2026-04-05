package domain

import "time"

type CompanyFinancials struct {
	ID                    string
	CompanyID             string
	BankName              string
	AccountName           string
	BSB                   string
	AccountNumber         string
	SwiftCode             string
	PaymentProvider       string
	PaymentProviderConfig map[string]any
	InvoicePrefix         string
	InvoiceCurrency       string
	InvoiceCounter        int
	CreatedAt             time.Time
}

type Invoice struct {
	ID             string
	CompanyID      string
	InvoiceNumber  string
	IssuedTo       string
	IssuedToEmail  string
	LineItems      []InvoiceLineItem
	TotalAmount    float64
	Currency       string
	Status         string
	Notes          string
	IssuedAt       string
	DueAt          string
}

type InvoiceLineItem struct {
	Description string
	Quantity    float64
	UnitPrice   float64
	Amount      float64
}

type Company struct {
	ID        string
	Name      string
	Mission   string
	Status    string
	CreatedAt time.Time
}

type Employee struct {
	ID              string
	CompanyID       string
	Name            string
	Role            string
	Specialties     string
	Type            string // ae|human
	Status          string
	SlackUserID     string
	ContainerID     string
	ContainerStatus string // none|running|stopped|error
	Config          *EmployeeConfig
	CreatedAt       time.Time
}

type EmployeeConfig struct {
	ID         string
	EmployeeID string
	Employee   struct {
		ID        string
		Role      string
		ReportsTo string
	}
	Identity struct {
		SoulFile string
	}
	Cognition struct {
		DefaultModelRef string
		Routing         map[string]string
	}
	Runtime struct {
		HeartbeatSeconds int
	}
	Tools map[string]bool
}

type OrgStructure struct {
	ID         string
	CompanyID  string
	EmployeeID string
	ReportsTo  string
	Department string
}

type MemoryEvent struct {
	ID         string
	EmployeeID string
	Type       string // episodic|reflection|heuristic
	Content    string
	Metadata   map[string]any
	CreatedAt  time.Time
}

type KnowledgebaseEntry struct {
	ID         string
	CompanyID  string
	Title      string
	Content    string
	Tags       []string
	Status     string
	ApprovedBy string
	CreatedAt  time.Time
}

type Task struct {
	ID            string
	CompanyID     string
	Title         string
	Description   string
	AssigneeID    string
	Status        string
	SlackThreadTS string
	SlackChannel  string
	CreatedAt     time.Time
}

type SlackEvent struct {
	ID          string
	CompanyID   string
	EventType   string
	Channel     string
	UserID      string
	ThreadTS    string
	MessageTS   string
	Payload     map[string]any
	ProcessedAt *time.Time
	CreatedAt   time.Time
}

type ToolLog struct {
	ID         string
	EmployeeID string
	Tool       string
	Action     string
	Input      map[string]any
	Output     map[string]any
	Success    bool
	TraceID    string
	TaskID     string
	CreatedAt  time.Time
}

type ModelConfig struct {
	ID            string
	Name          string
	ModelName     string // API-facing model name; defaults to ID if not set
	ProviderID    string
	ContextWindow int
	CreatedAt     time.Time
}

type ProviderConfig struct {
	ID        string
	Name      string
	APIStyle  string // openai|anthropic
	BaseURL   string
	APIKeyEnv string
	CreatedAt time.Time
}

type SlackMessageType int

const (
	SlackMessageIntent SlackMessageType = iota
	SlackMessageUpdate
	SlackMessageBlocker
	SlackMessageRequest
	SlackMessageDecision
)

type SlackMessage struct {
	Type       SlackMessageType
	AuthorID   string
	AuthorRole string
	Body       string
	Target     string
	Channel    string
}
