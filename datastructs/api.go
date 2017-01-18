// Package datastructs provides miscellaneous data structures for scotty.
package datastructs

import (
	"github.com/Symantec/Dominator/lib/mdb"
	"github.com/Symantec/scotty"
	"github.com/Symantec/scotty/sources"
	"github.com/Symantec/scotty/store"
	"io"
	"sort"
	"sync"
	"time"
)

// ApplicationStatus represents the status of a single application.
type ApplicationStatus struct {
	EndpointId *scotty.Endpoint
	Status     scotty.Status

	// The name of the endpoint's application
	Name string

	// True if this machine is active, false otherwise.
	Active bool

	// The zero value means no successful read
	LastReadTime time.Time
	// A zero value means no successful poll
	PollTime time.Duration

	// The last encountered error. nil mean no error.
	LastError error
	// Time of last encountered error. Zero value means no error.
	LastErrorTime time.Time

	// Initial metric count
	InitialMetricCount uint

	// Whether or not it is currently down.
	Down bool

	// If non-empty the AWS instance-id of the machine of the application
	InstanceId string

	changedMetrics_Sum   uint64
	changedMetrics_Count uint64
}

// Returns last error time as 2006-01-02T15:04:05
func (a *ApplicationStatus) LastErrorTimeStr() string {
	return a.LastErrorTime.Format("2006-01-02T15:04:05")
}

// AverageChangedMetrics returns how many metrics change value.
func (a *ApplicationStatus) AverageChangedMetrics() float64 {
	if a.changedMetrics_Count == 0 {
		return 0.0
	}
	return float64(a.changedMetrics_Sum) / float64(a.changedMetrics_Count)
}

// Staleness returns the staleness for this application.
// A zero value means no successful read happened.
func (a *ApplicationStatus) Staleness() time.Duration {
	if a.LastReadTime.IsZero() {
		return 0
	}
	return time.Now().Sub(a.LastReadTime)
}

// ByHostAndName sorts list by the hostname and then by application name
// in ascending order.
func ByHostAndName(list []*ApplicationStatus) {
	sort.Sort(byHostAndName(list))
}

// ApplicationStatuses is thread safe representation of application statuses
type ApplicationStatuses struct {
	appList *ApplicationList
	// A function must hold this lock when changing the status
	// (active vs. inactive) of the endpoints to ensure that when it
	// returns, the status of each endpoint matches the status of the
	// corresponding time series collection in the current store.
	// If this lock is acquired, it must be acquired before the normal
	// lock for this instance.
	statusChangeLock sync.Mutex
	// The normal lock for this instance
	lock sync.Mutex
	// The ApplicationStatus objects in the map are mutable to make
	// updates more memory efficient. lock protects each ApplicationStatus
	// object as well as the map itself.
	byEndpoint   map[*scotty.Endpoint]*ApplicationStatus
	byHostPort   map[hostAndPort]*scotty.Endpoint
	currentStore *store.Store
}

// NewApplicationStatuses creates a new ApplicationStatus instance.
// astore should be a newly initialized store.
// Since the newly created instance will create new store.Store instances
// whenever active machines change, caller should give up any reference it
// has to astore and use the Store() method to get the current store.
func NewApplicationStatuses(appList *ApplicationList, astore *store.Store) *ApplicationStatuses {
	return &ApplicationStatuses{
		appList:      appList,
		byEndpoint:   make(map[*scotty.Endpoint]*ApplicationStatus),
		byHostPort:   make(map[hostAndPort]*scotty.Endpoint),
		currentStore: astore,
	}
}

// Store returns the current store instance.
func (a *ApplicationStatuses) Store() *store.Store {
	return a.store()
}

// ApplicationList returns the application list of this instance.
func (a *ApplicationStatuses) ApplicationList() *ApplicationList {
	return a.appList
}

// Update updates the status of a single application / endpoint.
// This instance must have prior knowledge of e. That is, e must come from
// a method such as ActiveEndpointIds(). Otherwise, this method panics.
func (a *ApplicationStatuses) Update(
	e *scotty.Endpoint, newState *scotty.State) {
	a.update(e, newState)
}

// ReportError reports an error for the given endpoint.
// This instance must have prior knowledge of e. That is, e must come from
// a method such as ActiveEndpointIds(). Otherwise, this method panics.
// To clear the error for an endpoint, caller passes nil for err. ts is the
// time the error occurred, or if err == nil, the time it was cleared.
func (a *ApplicationStatuses) ReportError(
	e *scotty.Endpoint, err error, ts time.Time) {
	a.reportError(e, err, ts)
}

// MarkHostsActiveExclusively marks all applications / endpoints of each host
// within activeHosts as active while marking the rest inactive.
// MarkHostsActiveExclusively marks all time series of any machines that just
// became inactive as inactive by adding an inactive flag to each one with
// given time stamp.
// timestamp is seconds since Jan 1, 1970 GMT.
func (a *ApplicationStatuses) MarkHostsActiveExclusively(
	timestamp float64, activeHosts []mdb.Machine) {
	a.markHostsActiveExclusively(timestamp, activeHosts)
}

// LogChangedMetricCount logs how many metrics changed for a given
// application / endpoint.
// This instance must have prior knowledge of e. That is, e must come from
// a method such as ActiveEndpointIds(). Otherwise, this method panics.
func (a *ApplicationStatuses) LogChangedMetricCount(
	e *scotty.Endpoint, metricCount uint) {
	a.logChangedMetricCount(e, metricCount)
}

// All returns the statuses for all the applications.
// Clients are free to reorder the returned slice.
func (a *ApplicationStatuses) All() []*ApplicationStatus {
	return a.all()
}

// AllWithStore works like All, but also returns the current store.
func (a *ApplicationStatuses) AllWithStore() (
	[]*ApplicationStatus, *store.Store) {
	return a.allWithStore(nil)
}

// AllActiveWithStore is like AllWithStore but returns only the statuses
// where Active is true.
func (a *ApplicationStatuses) AllActiveWithStore() (
	[]*ApplicationStatus, *store.Store) {
	return a.allWithStore(
		func(s *ApplicationStatus) bool { return s.Active })
}

func (a *ApplicationStatuses) ByEndpointId(
	e *scotty.Endpoint) *ApplicationStatus {
	return a.byEndpointId(e)
}

// EndpointIdByHostAndName Returns the endpoint Id for given host and
// application name along with the current store. If no such host and
// application name combo exists, returns nil and the curent store.
func (a *ApplicationStatuses) EndpointIdByHostAndName(
	host, name string) (*scotty.Endpoint, *store.Store) {
	return a.endpointIdByHostAndName(host, name)
}

// ActiveEndpointIds  Returns all the active endpoints
// along with the current store.
func (a *ApplicationStatuses) ActiveEndpointIds() (
	[]*scotty.Endpoint, *store.Store) {
	return a.activeEndpointIds()
}

// Application represents a particular application in the fleet.
// Application instances are immutable.
type Application struct {
	name       string
	connectors sources.ConnectorList
	port       uint
}

// Name returns the name of application
func (a *Application) Name() string {
	return a.name
}

// Connector returns the connector for the application
func (a *Application) Connectors() sources.ConnectorList {
	return a.connectors
}

// Port returns the port number of the application
func (a *Application) Port() uint {
	return a.port
}

// ApplicationList represents all the applications in the fleet.
// ApplicationList instances are immutable.
type ApplicationList struct {
	byPort map[uint]*Application
	byName map[string]*Application
}

// All lists all the applications in no particular order.
// Clients may reorder returned slice.
func (a *ApplicationList) All() []*Application {
	return a.all()
}

// ByPort returns the application running on a particular port or nil
// if no application is using port.
func (a *ApplicationList) ByPort(port uint) *Application {
	return a.byPort[port]
}

// ByPort returns the application with a particular name or nil if no such
// application exists.
func (a *ApplicationList) ByName(name string) *Application {
	return a.byName[name]
}

// ApplicationListBuilder builds an ApplicationList instance.
type ApplicationListBuilder struct {
	listPtr **ApplicationList
}

func NewApplicationListBuilder() *ApplicationListBuilder {
	return newApplicationListBuilder()
}

// Add adds an application.
func (a *ApplicationListBuilder) Add(
	port uint, applicationName string, connectors sources.ConnectorList) {
	a.add(port, applicationName, connectors)
}

// Read application names and ports from a config file into this builder.
func (a *ApplicationListBuilder) ReadConfig(r io.Reader) error {
	return a.readConfig(r)
}

// Build builds the ApplicationList instance and destroys this builder.
func (a *ApplicationListBuilder) Build() *ApplicationList {
	return a.build()
}
