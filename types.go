package k8dex

import "encoding/json"

// Options selects the Kubernetes API knowledge included in a system prompt.
type Options struct {
	KubernetesVersion string
}

// ContextMode controls whether a prompt uses focused or complete API metadata.
type ContextMode string

const (
	ContextFocused  ContextMode = "focused"
	ContextFull     ContextMode = "full"
	ContextRejected ContextMode = "rejected"
)

// IntentKind describes whether a query is an actionable Kubernetes request.
type IntentKind string

const (
	IntentKubernetesOperation IntentKind = "kubernetes_operation"
	IntentNonKubernetes       IntentKind = "non_kubernetes"
	IntentNonOperational      IntentKind = "non_operational"
)

// IntentAction identifies the deterministic action inferred from user text.
type IntentAction string

const (
	ActionList   IntentAction = "list"
	ActionCount  IntentAction = "count"
	ActionGet    IntentAction = "get"
	ActionPatch  IntentAction = "patch"
	ActionDelete IntentAction = "delete"
)

// IntentRelation identifies a known Kubernetes object relationship.
type IntentRelation string

const (
	RelationServiceBackends IntentRelation = "service_backends"
	RelationWorkloadPods    IntentRelation = "workload_pods"
	RelationWorkloadScale   IntentRelation = "workload_scale"
)

// Intent is a normalized, provider-independent request for focused prompt
// construction. Consumers may create it with their own classifier.
type Intent struct {
	Action        IntentAction      `json:"action"`
	ResourceKinds []string          `json:"resourceKinds"`
	References    []ObjectReference `json:"references,omitempty"`
	Namespace     string            `json:"namespace,omitempty"`
	Relation      IntentRelation    `json:"relation,omitempty"`
}

// IntentDescriptor documents a supported intent template for consumer-side
// normalization, UI help, or another classifier.
type IntentDescriptor struct {
	Name          string         `json:"name"`
	Description   string         `json:"description"`
	Actions       []IntentAction `json:"actions"`
	ResourceKinds []string       `json:"resourceKinds,omitempty"`
	Relation      IntentRelation `json:"relation,omitempty"`
	Examples      []string       `json:"examples,omitempty"`
}

// IntentCandidate is a scored resource family considered by the router.
type IntentCandidate struct {
	Kind    string   `json:"kind"`
	Score   float64  `json:"score"`
	Reasons []string `json:"reasons,omitempty"`
}

// ObjectReference is an explicitly named Kubernetes object found in a query.
type ObjectReference struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// IntentAnalysis is the deterministic routing result used to select prompt
// context. Confidence is a routing score, not a probability.
type IntentAnalysis struct {
	Action        IntentAction      `json:"action,omitempty"`
	Kind          IntentKind        `json:"kind"`
	Candidates    []IntentCandidate `json:"candidates,omitempty"`
	SelectedKinds []string          `json:"selectedKinds,omitempty"`
	Confidence    float64           `json:"confidence"`
	Margin        float64           `json:"margin"`
	Mode          ContextMode       `json:"mode"`
	MissingFields []string          `json:"missingFields,omitempty"`
	References    []ObjectReference `json:"references,omitempty"`
	Evidence      []string          `json:"evidence,omitempty"`
}

// Operation is one Kubernetes HTTP request proposed by an external model.
type Operation struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Query   map[string]string `json:"query"`
	Headers map[string]string `json:"headers"`
	Body    json.RawMessage   `json:"body"`
}

// ResponseError is a structured model failure.
type ResponseError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ModelResponse is the provider-independent output contract generated prompts
// require from an external model.
type ModelResponse struct {
	Operations []Operation    `json:"operations"`
	Error      *ResponseError `json:"error"`
}
