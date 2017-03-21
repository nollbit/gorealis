/**
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// Package realis provides the ability to use Thrift API to communicate with Apache Aurora.
package realis

import (
	"encoding/base64"
	"net/http"
	"net/http/cookiejar"
	"time"

	"fmt"

	"math/rand"

	"git.apache.org/thrift.git/lib/go/thrift"
	"github.com/pkg/errors"
	"github.com/rdelval/gorealis/gen-go/apache/aurora"
	"github.com/rdelval/gorealis/response"
)

type Realis interface {
	AbortJobUpdate(updateKey aurora.JobUpdateKey, message string) (*aurora.Response, error)
	AddInstances(instKey aurora.InstanceKey, count int32) (*aurora.Response, error)
	RemoveInstances(key *aurora.JobKey, count int32) (*aurora.Response, error)
	CreateJob(auroraJob Job) (*aurora.Response, error)
	DescheduleCronJob(key *aurora.JobKey) (*aurora.Response, error)
	GetTaskStatus(query *aurora.TaskQuery) ([]*aurora.ScheduledTask, error)
	FetchTaskConfig(instKey aurora.InstanceKey) (*aurora.TaskConfig, error)
	GetInstanceIds(key *aurora.JobKey, states map[aurora.ScheduleStatus]bool) (map[int32]bool, error)
	JobUpdateDetails(updateQuery aurora.JobUpdateQuery) (*aurora.Response, error)
	KillJob(key *aurora.JobKey) (*aurora.Response, error)
	KillInstances(key *aurora.JobKey, instances ...int32) (*aurora.Response, error)
	RestartInstances(key *aurora.JobKey, instances ...int32) (*aurora.Response, error)
	RestartJob(key *aurora.JobKey) (*aurora.Response, error)
	RollbackJobUpdate(key aurora.JobUpdateKey, message string) (*aurora.Response, error)
	ScheduleCronJob(auroraJob Job) (*aurora.Response, error)
	StartJobUpdate(updateJob *UpdateJob, message string) (*aurora.Response, error)
	StartCronJob(key *aurora.JobKey) (*aurora.Response, error)
	GetJobUpdateSummaries(jobUpdateQuery *aurora.JobUpdateQuery) (*aurora.Response, error)
	ReestablishConn() error
	Close()
}

type realisClient struct {
	config         *RealisConfig
	client         *aurora.AuroraSchedulerManagerClient
	readonlyClient *aurora.ReadOnlySchedulerClient
}

// Wrapper object to provide future flexibility
type RealisConfig struct {
	username, password string
	url                string
	cluster            *Cluster
	transport          thrift.TTransport
	protoFactory       thrift.TProtocolFactory
}

type Backoff struct {
	Duration time.Duration // the base duration
	Factor   float64       // Duration is multipled by factor each iteration
	Jitter   float64       // The amount of jitter applied each iteration
	Steps    int           // Exit with error after this many steps
}

var defaultBackoff = Backoff{
	Steps:    3,
	Duration: 10 * time.Second,
	Factor:   5.0,
	Jitter:   0.1,
}

// Jitter returns a time.Duration between duration and duration + maxFactor *
// duration.
//
// This allows clients to avoid converging on periodic behavior. If maxFactor
// is 0.0, a suggested default value will be chosen.
func Jitter(duration time.Duration, maxFactor float64) time.Duration {
	if maxFactor <= 0.0 {
		maxFactor = 1.0
	}
	wait := duration + time.Duration(rand.Float64()*maxFactor*float64(duration))
	return wait
}

// Create a new Client with Cluster information and other details.

func NewDefaultClientUsingCluster(cluster *Cluster, user, passwd string) (Realis, error) {

	url, err := LeaderFromZK(*cluster)
	if err != nil {
		fmt.Println(err)
		return nil, err
	}
	fmt.Printf(" url: %s\n", url)

	//Create new configuration with default transport layer
	config, err := NewDefaultConfig("http://localhost:18000", 10000)
	if err != nil {
		fmt.Println(err)
		return nil, err
	}
	config.username = user
	config.password = passwd
	config.cluster = cluster
	config.url = ""
	// Configured for vagrant
	AddBasicAuth(config, user, passwd)
	r := NewClient(config)
	return r, nil
}

//This api would create default cluster object..
func NewDefaultClientUsingZKUrl(zkUrl, user, passwd string) (Realis, error) {

	fmt.Println(" zkUrl: %s", zkUrl)
	cluster := &Cluster{Name: "testCluster",
		AuthMechanism: "UNAUTHENTICATED",
		ZK:            zkUrl,
		SchedZKPath:   "/aurora/scheduler",
		AgentRunDir:   "latest",
		AgentRoot:     "/var/lib/mesos",
	}

	url, err := LeaderFromZK(*cluster)
	if err != nil {
		fmt.Println(err)
		return nil, err
	}
	fmt.Printf(" url: %s\n", url)

	//Create new configuration with default transport layer
	config, err := NewDefaultConfig("http://localhost:18000", 10000)
	if err != nil {
		fmt.Println(err)
		return nil, err
	}
	config.username = user
	config.password = passwd
	config.cluster = cluster
	config.url = ""
	// Configured for vagrant
	AddBasicAuth(config, user, passwd)
	r := NewClient(config)
	return r, nil
}

func NewDefaultClientUsingUrl(url, user, passwd string) (Realis, error) {

	fmt.Printf(" url: %s\n", url)
	//Create new configuration with default transport layer
	config, err := NewDefaultConfig("http://localhost:18000", 10000)
	if err != nil {
		fmt.Println(err)
		return nil, err
	}
	config.username = user
	config.password = passwd
	config.url = url
	config.cluster = nil
	// Configured for vagrant
	AddBasicAuth(config, user, passwd)
	r := NewClient(config)
	return r, nil
}

// Create a new Client with a default transport layer
func NewClient(realisconfig *RealisConfig) Realis {
	return &realisClient{
		config:         realisconfig,
		client:         aurora.NewAuroraSchedulerManagerClientFactory(realisconfig.transport, realisconfig.protoFactory),
		readonlyClient: aurora.NewReadOnlySchedulerClientFactory(realisconfig.transport, realisconfig.protoFactory)}
}

// Creates a default Thrift Transport object for communications in gorealis using an HTTP Post Client
func defaultTTransport(urlstr string, timeoutms int) (thrift.TTransport, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return &thrift.THttpClient{}, errors.Wrap(err, "Error creating Cookie Jar")
	}

	trans, err := thrift.NewTHttpPostClientWithOptions(urlstr+"/api",
		thrift.THttpClientOptions{Client: &http.Client{Timeout: time.Millisecond * time.Duration(timeoutms), Jar: jar}})

	if err != nil {
		return &thrift.THttpClient{}, errors.Wrap(err, "Error creating transport")
	}

	if err := trans.Open(); err != nil {
		return &thrift.THttpClient{}, errors.Wrapf(err, "Error opening connection to %s", urlstr)
	}

	return trans, nil
}

// Create a default configuration of the transport layer, requires a URL to test connection with.
// Uses HTTP Post as transport layer and Thrift JSON as the wire protocol by default.
func NewDefaultConfig(url string, timeoutms int) (*RealisConfig, error) {
	return NewTJSONConfig(url, timeoutms)
}

// Creates a realis config object using HTTP Post and Thrift JSON protocol to communicate with Aurora.
func NewTJSONConfig(url string, timeoutms int) (*RealisConfig, error) {
	trans, err := defaultTTransport(url, timeoutms)
	if err != nil {
		return &RealisConfig{}, errors.Wrap(err, "Error creating realis config")
	}

	httpTrans := (trans).(*thrift.THttpClient)
	httpTrans.SetHeader("Content-Type", "application/x-thrift")

	return &RealisConfig{transport: trans, protoFactory: thrift.NewTJSONProtocolFactory()}, nil
}

// Creates a realis config config using HTTP Post and Thrift Binary protocol to communicate with Aurora.
func NewTBinaryConfig(url string, timeoutms int) (*RealisConfig, error) {
	trans, err := defaultTTransport(url, timeoutms)
	if err != nil {
		return &RealisConfig{}, errors.Wrap(err, "Error creating realis config")
	}

	httpTrans := (trans).(*thrift.THttpClient)
	httpTrans.SetHeader("Accept", "application/vnd.apache.thrift.binary")
	httpTrans.SetHeader("Content-Type", "application/vnd.apache.thrift.binary")
	httpTrans.SetHeader("User-Agent", "GoRealis v1.0.4")

	return &RealisConfig{transport: trans, protoFactory: thrift.NewTBinaryProtocolFactoryDefault()}, nil

}

// Helper function to add basic authorization needed to communicate with Apache Aurora.
func AddBasicAuth(config *RealisConfig, username string, password string) {
	config.username = username
	config.password = password
	httpTrans := (config.transport).(*thrift.THttpClient)
	httpTrans.SetHeader("Authorization", "Basic "+basicAuth(username, password))
}

func basicAuth(username, password string) string {
	auth := username + ":" + password
	return base64.StdEncoding.EncodeToString([]byte(auth))
}

func (r *realisClient) ReestablishConn() error {
	//close existing connection..
	fmt.Println("ReestablishConn begin ....")
	r.Close()
	//First check cluster object for re-establish; if not available then try with scheduler url.
	if r.config.cluster != nil && r.config.username != "" && r.config.password != "" {
		//Re-establish using cluster object.
		url, err := LeaderFromZK(*r.config.cluster)
		if err != nil {
			fmt.Errorf("LeaderFromZK error: %+v\n ", err)
		}
		fmt.Println("ReestablishConn url: ", url)
		config, err := NewDefaultConfig(url, 10000)
		if err != nil {
			fmt.Println(err)
		}
		// Configured for basic-auth
		AddBasicAuth(config, r.config.username, r.config.password)
		config.cluster = r.config.cluster
		r.config = config
		r.client = aurora.NewAuroraSchedulerManagerClientFactory(config.transport, config.protoFactory)
		r.readonlyClient = aurora.NewReadOnlySchedulerClientFactory(config.transport, config.protoFactory)
	} else if r.config.url != "" && r.config.username != "" && r.config.password != "" {
		//Re-establish using scheduler url.
		//Create new configuration with default transport layer
		config, err := NewDefaultConfig(r.config.url, 10000)
		if err != nil {
			fmt.Println(err)
		}
		AddBasicAuth(config, r.config.username, r.config.password)
		r.config = config
		r.client = aurora.NewAuroraSchedulerManagerClientFactory(config.transport, config.protoFactory)
		r.readonlyClient = aurora.NewReadOnlySchedulerClientFactory(config.transport, config.protoFactory)
	} else {
		fmt.Println(" Missing Data for ReestablishConn ")
		fmt.Println(" r.config.cluster: ", r.config.cluster)
		fmt.Println(" r.config.username: ", r.config.username)
		fmt.Println(" r.config.passwd: ", r.config.password)
		fmt.Println(" r.config.url: ", r.config.url)
		return errors.New(" Missing Data for ReestablishConn ")
	}
	return nil
}

// Releases resources associated with the realis client.
func (r *realisClient) Close() {
	r.client.Transport.Close()
	r.readonlyClient.Transport.Close()
}

// Uses predefined set of states to retrieve a set of active jobs in Apache Aurora.
func (r *realisClient) GetInstanceIds(key *aurora.JobKey, states map[aurora.ScheduleStatus]bool) (map[int32]bool, error) {
	taskQ := &aurora.TaskQuery{Role: key.Role,
		Environment: key.Environment,
		JobName:     key.Name,
		Statuses:    states}

	var resp *aurora.Response
	var err error

	duration := defaultBackoff.Duration
	for i := 0; i < defaultBackoff.Steps; i++ {
		if i != 0 {
			adjusted := duration
			if defaultBackoff.Jitter > 0.0 {
				adjusted = Jitter(duration, defaultBackoff.Jitter)
			}
			fmt.Println(" sleeping for: ", adjusted)
			time.Sleep(adjusted)
			duration = time.Duration(float64(duration) * defaultBackoff.Factor)
		}
		if resp, err = r.client.GetTasksWithoutConfigs(taskQ); err == nil {
			fmt.Println("resp: ", resp)
			break
		}
		err1 := r.ReestablishConn()
		if err1 != nil {
			fmt.Println("error in ReestablishConn: ", err1)
		}
	}

	if err != nil {
		return nil, errors.Wrap(err, "Error querying Aurora Scheduler for active IDs")
	}

	tasks := response.ScheduleStatusResult(resp).GetTasks()

	jobInstanceIds := make(map[int32]bool)
	for _, task := range tasks {
		jobInstanceIds[task.GetAssignedTask().GetInstanceId()] = true
	}

	return jobInstanceIds, nil
}

func (r *realisClient) GetJobUpdateSummaries(jobUpdateQuery *aurora.JobUpdateQuery) (*aurora.Response, error) {
	var resp *aurora.Response
	var err error

	duration := defaultBackoff.Duration
	for i := 0; i < defaultBackoff.Steps; i++ {
		if i != 0 {
			adjusted := duration
			if defaultBackoff.Jitter > 0.0 {
				adjusted = Jitter(duration, defaultBackoff.Jitter)
			}
			fmt.Println(" sleeping for: ", adjusted)
			time.Sleep(adjusted)
			duration = time.Duration(float64(duration) * defaultBackoff.Factor)
		}
		if resp, err = r.readonlyClient.GetJobUpdateSummaries(jobUpdateQuery); err == nil {
			fmt.Println("resp: ", resp)
			return response.ResponseCodeCheck(resp)
		}
		err1 := r.ReestablishConn()
		if err1 != nil {
			fmt.Println("error in ReestablishConn: ", err1)
		}

	}
	return nil, errors.Wrap(err, "Error getting job update summaries from Aurora Scheduler")
}

// Kill specific instances of a job.
func (r *realisClient) KillInstances(key *aurora.JobKey, instances ...int32) (*aurora.Response, error) {

	instanceIds := make(map[int32]bool)
	var resp *aurora.Response
	var err error
	duration := defaultBackoff.Duration

	for _, instId := range instances {
		instanceIds[instId] = true
	}

	for i := 0; i < defaultBackoff.Steps; i++ {
		if i != 0 {
			adjusted := duration
			if defaultBackoff.Jitter > 0.0 {
				adjusted = Jitter(duration, defaultBackoff.Jitter)
			}
			fmt.Println(" sleeping for: ", adjusted)
			time.Sleep(adjusted)
			duration = time.Duration(float64(duration) * defaultBackoff.Factor)
		}
		if resp, err = r.client.KillTasks(key, instanceIds); err == nil {
			fmt.Println("resp: ", resp)
			return response.ResponseCodeCheck(resp)
		}
		err1 := r.ReestablishConn()
		if err1 != nil {
			fmt.Println("error in ReestablishConn: ", err1)
		}

	}
	return nil, errors.Wrap(err, "Error sending Kill command to Aurora Scheduler")
}

// Sends a kill message to the scheduler for all active tasks under a job.
func (r *realisClient) KillJob(key *aurora.JobKey) (*aurora.Response, error) {

	var instanceIds map[int32]bool
	var err error
	var resp *aurora.Response
	instanceIds, err = r.GetInstanceIds(key, aurora.ACTIVE_STATES)
	if err != nil {
		return nil, errors.Wrap(err, "Could not retrieve relevant task instance IDs")
	}

	if len(instanceIds) > 0 {

		duration := defaultBackoff.Duration
		for i := 0; i < defaultBackoff.Steps; i++ {
			if i != 0 {
				adjusted := duration
				if defaultBackoff.Jitter > 0.0 {
					adjusted = Jitter(duration, defaultBackoff.Jitter)
				}
				fmt.Println(" sleeping for: ", adjusted)
				time.Sleep(adjusted)
				duration = time.Duration(float64(duration) * defaultBackoff.Factor)
			}

			if resp, err = r.client.KillTasks(key, instanceIds); err == nil {
				return response.ResponseCodeCheck(resp)
			}

			err1 := r.ReestablishConn()
			if err1 != nil {
				fmt.Println("error in ReestablishConn: ", err1)
			}
		}
		if err != nil {
			return nil, errors.Wrap(err, "Error sending Kill command to Aurora Scheduler")
		}
	}
	return nil, errors.New("No tasks in the Active state")
}

// Sends a create job message to the scheduler with a specific job configuration.
func (r *realisClient) CreateJob(auroraJob Job) (*aurora.Response, error) {
	var resp *aurora.Response
	var err error

	duration := defaultBackoff.Duration
	for i := 0; i < defaultBackoff.Steps; i++ {
		if i != 0 {
			adjusted := duration
			if defaultBackoff.Jitter > 0.0 {
				adjusted = Jitter(duration, defaultBackoff.Jitter)
			}
			fmt.Println(" sleeping for: ", adjusted)
			time.Sleep(adjusted)
			duration = time.Duration(float64(duration) * defaultBackoff.Factor)
		}

		if resp, err = r.client.CreateJob(auroraJob.JobConfig()); err == nil {
			return response.ResponseCodeCheck(resp)
		}
		err1 := r.ReestablishConn()
		if err1 != nil {
			fmt.Println("error in ReestablishConn: ", err1)
		}
	}
	return nil, errors.Wrap(err, "Error sending Create command to Aurora Scheduler")
}

func (r *realisClient) ScheduleCronJob(auroraJob Job) (*aurora.Response, error) {
	var resp *aurora.Response
	var err error

	duration := defaultBackoff.Duration
	for i := 0; i < defaultBackoff.Steps; i++ {
		if i != 0 {
			adjusted := duration
			if defaultBackoff.Jitter > 0.0 {
				adjusted = Jitter(duration, defaultBackoff.Jitter)
			}
			fmt.Println(" sleeping for: ", adjusted)
			time.Sleep(adjusted)
			duration = time.Duration(float64(duration) * defaultBackoff.Factor)
		}

		if resp, err = r.client.ScheduleCronJob(auroraJob.JobConfig()); err == nil {
			return response.ResponseCodeCheck(resp)
		}
		err1 := r.ReestablishConn()
		if err1 != nil {
			fmt.Println("error in ReestablishConn: ", err1)
		}
	}
	return nil, errors.Wrap(err, "Error sending Cron Job Schedule message to Aurora Scheduler")
}

func (r *realisClient) DescheduleCronJob(key *aurora.JobKey) (*aurora.Response, error) {

	var resp *aurora.Response
	var err error

	duration := defaultBackoff.Duration
	for i := 0; i < defaultBackoff.Steps; i++ {
		if i != 0 {
			adjusted := duration
			if defaultBackoff.Jitter > 0.0 {
				adjusted = Jitter(duration, defaultBackoff.Jitter)
			}
			fmt.Println(" sleeping for: ", adjusted)
			time.Sleep(adjusted)
			duration = time.Duration(float64(duration) * defaultBackoff.Factor)
		}

		if resp, err = r.client.DescheduleCronJob(key); err == nil {
			return response.ResponseCodeCheck(resp)
		}
		err1 := r.ReestablishConn()
		if err1 != nil {
			fmt.Println("error in ReestablishConn: ", err1)
		}
	}

	return nil, errors.Wrap(err, "Error sending Cron Job De-schedule message to Aurora Scheduler")
}

func (r *realisClient) StartCronJob(key *aurora.JobKey) (*aurora.Response, error) {
	var resp *aurora.Response
	var err error

	duration := defaultBackoff.Duration
	for i := 0; i < defaultBackoff.Steps; i++ {
		if i != 0 {
			adjusted := duration
			if defaultBackoff.Jitter > 0.0 {
				adjusted = Jitter(duration, defaultBackoff.Jitter)
			}
			fmt.Println(" sleeping for: ", adjusted)
			time.Sleep(adjusted)
			duration = time.Duration(float64(duration) * defaultBackoff.Factor)
		}

		if resp, err = r.client.StartCronJob(key); err == nil {
			return response.ResponseCodeCheck(resp)
		}
		err1 := r.ReestablishConn()
		if err1 != nil {
			fmt.Println("error in ReestablishConn: ", err1)
		}
	}

	return nil, errors.Wrap(err, "Error sending Start Cron Job  message to Aurora Scheduler")
}

// Restarts specific instances specified
func (r *realisClient) RestartInstances(key *aurora.JobKey, instances ...int32) (*aurora.Response, error) {
	instanceIds := make(map[int32]bool)

	for _, instId := range instances {
		instanceIds[instId] = true
	}
	var resp *aurora.Response
	var err error
	duration := defaultBackoff.Duration
	for i := 0; i < defaultBackoff.Steps; i++ {
		if i != 0 {
			adjusted := duration
			if defaultBackoff.Jitter > 0.0 {
				adjusted = Jitter(duration, defaultBackoff.Jitter)
			}
			fmt.Println(" sleeping for: ", adjusted)
			time.Sleep(adjusted)
			duration = time.Duration(float64(duration) * defaultBackoff.Factor)
		}

		if resp, err = r.client.RestartShards(key, instanceIds); err == nil {
			return response.ResponseCodeCheck(resp)
		}
		err1 := r.ReestablishConn()
		if err1 != nil {
			fmt.Println("error in ReestablishConn: ", err1)
		}

	}
	return nil, errors.Wrap(err, "Error sending Restart command to Aurora Scheduler")
}

// Restarts all active tasks under a job configuration.
func (r *realisClient) RestartJob(key *aurora.JobKey) (*aurora.Response, error) {

	instanceIds, err1 := r.GetInstanceIds(key, aurora.ACTIVE_STATES)
	if err1 != nil {
		return nil, errors.Wrap(err1, "Could not retrieve relevant task instance IDs")
	}

	var resp *aurora.Response
	var err error
	if len(instanceIds) > 0 {
		duration := defaultBackoff.Duration
		for i := 0; i < defaultBackoff.Steps; i++ {
			if i != 0 {
				adjusted := duration
				if defaultBackoff.Jitter > 0.0 {
					adjusted = Jitter(duration, defaultBackoff.Jitter)
				}
				fmt.Println(" sleeping for: ", adjusted)
				time.Sleep(adjusted)
				duration = time.Duration(float64(duration) * defaultBackoff.Factor)
			}

			if resp, err = r.client.RestartShards(key, instanceIds); err == nil {
				return response.ResponseCodeCheck(resp)
			}
			err1 := r.ReestablishConn()
			if err1 != nil {
				fmt.Println("error in ReestablishConn: ", err1)
			}

		}
		return nil, errors.Wrap(err, "Error sending Restart command to Aurora Scheduler")

	} else {
		return nil, errors.New("No tasks in the Active state")
	}
}

// Update all tasks under a job configuration. Currently gorealis doesn't support for canary deployments.
func (r *realisClient) StartJobUpdate(updateJob *UpdateJob, message string) (*aurora.Response, error) {

	var resp *aurora.Response
	var err error

	duration := defaultBackoff.Duration
	for i := 0; i < defaultBackoff.Steps; i++ {
		if i != 0 {
			adjusted := duration
			if defaultBackoff.Jitter > 0.0 {
				adjusted = Jitter(duration, defaultBackoff.Jitter)
			}
			fmt.Println(" sleeping for: ", adjusted)
			time.Sleep(adjusted)
			duration = time.Duration(float64(duration) * defaultBackoff.Factor)
		}
		if resp, err = r.client.StartJobUpdate(updateJob.req, message); err == nil {
			return response.ResponseCodeCheck(resp)
		}
		err1 := r.ReestablishConn()
		if err1 != nil {
			fmt.Println("error in ReestablishConn: ", err1)
		}
	}
	return nil, errors.Wrap(err, "Error sending StartJobUpdate command to Aurora Scheduler")
}

// Abort Job Update on Aurora. Requires the updateId which can be obtained on the Aurora web UI.
func (r *realisClient) AbortJobUpdate(
	updateKey aurora.JobUpdateKey,
	message string) (*aurora.Response, error) {

	var resp *aurora.Response
	var err error
	duration := defaultBackoff.Duration
	for i := 0; i < defaultBackoff.Steps; i++ {
		if i != 0 {
			adjusted := duration
			if defaultBackoff.Jitter > 0.0 {
				adjusted = Jitter(duration, defaultBackoff.Jitter)
			}
			fmt.Println(" sleeping for: ", adjusted)
			time.Sleep(adjusted)
			duration = time.Duration(float64(duration) * defaultBackoff.Factor)
		}
		if resp, err = r.client.AbortJobUpdate(&updateKey, message); err == nil {
			return response.ResponseCodeCheck(resp)
		}
		err1 := r.ReestablishConn()
		if err1 != nil {
			fmt.Println("error in ReestablishConn: ", err1)
		}
	}

	return nil, errors.Wrap(err, "Error sending AbortJobUpdate command to Aurora Scheduler")
}

// Scale up the number of instances under a job configuration using the configuration for specific
// instance to scale up.
func (r *realisClient) AddInstances(instKey aurora.InstanceKey, count int32) (*aurora.Response, error) {

	var resp *aurora.Response
	var err error
	duration := defaultBackoff.Duration
	for i := 0; i < defaultBackoff.Steps; i++ {
		if i != 0 {
			adjusted := duration
			if defaultBackoff.Jitter > 0.0 {
				adjusted = Jitter(duration, defaultBackoff.Jitter)
			}
			fmt.Println(" sleeping for: ", adjusted)
			time.Sleep(adjusted)
			duration = time.Duration(float64(duration) * defaultBackoff.Factor)
		}
		if resp, err = r.client.AddInstances(&instKey, count); err == nil {
			return response.ResponseCodeCheck(resp)
		}
		err1 := r.ReestablishConn()
		if err1 != nil {
			fmt.Println("error in ReestablishConn: ", err1)
		}
	}
	return nil, errors.Wrap(err, "Error sending AddInstances command to Aurora Scheduler")
}

//Scale down the number of instances under a job configuration using the configuratipn of a specific instance
func (r *realisClient) RemoveInstances(key *aurora.JobKey, count int32) (*aurora.Response, error) {
	instanceIds, err := r.GetInstanceIds(key, aurora.ACTIVE_STATES)
	if err != nil {
		return nil, errors.Wrap(err, "RemoveInstances: Could not retrieve relevant instance IDs")
	}
	if len(instanceIds) < int(count) {
		return nil, errors.New(fmt.Sprintf("RemoveInstances: No sufficient instances to Kill - "+
			"Instances to kill %d Total Instances %d", count, len(instanceIds)))
	}
	instanceList := make([]int32, count)
	i := 0
	for k := range instanceIds {
		instanceList[i] = k
		i += 1
		if i == int(count) {
			break
		}
	}
	return r.KillInstances(key, instanceList...)
}

func (r *realisClient) GetTaskStatus(query *aurora.TaskQuery) (tasks []*aurora.ScheduledTask, e error) {

	var resp *aurora.Response
	var err error
	duration := defaultBackoff.Duration
	for i := 0; i < defaultBackoff.Steps; i++ {
		if i != 0 {
			adjusted := duration
			if defaultBackoff.Jitter > 0.0 {
				adjusted = Jitter(duration, defaultBackoff.Jitter)
			}
			fmt.Println(" sleeping for: ", adjusted)
			time.Sleep(adjusted)
			duration = time.Duration(float64(duration) * defaultBackoff.Factor)
		}
		if resp, err = r.client.GetTasksStatus(query); err == nil {
			break
		}
		err1 := r.ReestablishConn()
		if err1 != nil {
			fmt.Println("error in ReestablishConn: ", err1)
		}
	}

	if err != nil {
		return nil, errors.Wrap(err, "Error querying Aurora Scheduler for task status")
	}
	//Check for response code..
	if resp.GetResponseCode() != aurora.ResponseCode_OK {
		return nil, errors.New(resp.ResponseCode.String() + "--" + response.CombineMessage(resp))
	}

	return response.ScheduleStatusResult(resp).GetTasks(), nil
}

func (r *realisClient) FetchTaskConfig(instKey aurora.InstanceKey) (*aurora.TaskConfig, error) {

	ids := make(map[int32]bool)

	ids[instKey.InstanceId] = true
	taskQ := &aurora.TaskQuery{Role: instKey.JobKey.Role,
		Environment: instKey.JobKey.Environment,
		JobName:     instKey.JobKey.Name,
		InstanceIds: ids,
		Statuses:    aurora.ACTIVE_STATES}

	var resp *aurora.Response
	var err error

	duration := defaultBackoff.Duration
	for i := 0; i < defaultBackoff.Steps; i++ {
		if i != 0 {
			adjusted := duration
			if defaultBackoff.Jitter > 0.0 {
				adjusted = Jitter(duration, defaultBackoff.Jitter)
			}
			fmt.Println(" sleeping for: ", adjusted)
			time.Sleep(adjusted)
			duration = time.Duration(float64(duration) * defaultBackoff.Factor)
		}

		if resp, err = r.client.GetTasksStatus(taskQ); err == nil {
			fmt.Println("resp: ", resp)
			break
		}
		err1 := r.ReestablishConn()
		if err1 != nil {
			fmt.Println("error in ReestablishConn: ", err1)
		}
	}

	if err != nil {
		return nil, errors.Wrap(err, "Error querying Aurora Scheduler for task configuration")
	}

	//Check for response code..
	if resp.GetResponseCode() != aurora.ResponseCode_OK {
		return nil, errors.New(resp.ResponseCode.String() + "--" + response.CombineMessage(resp))
	}

	tasks := response.ScheduleStatusResult(resp).GetTasks()

	if len(tasks) == 0 {
		return nil, errors.Errorf("Instance %d for jobkey %s/%s/%s doesn't exist",
			instKey.InstanceId,
			instKey.JobKey.Environment,
			instKey.JobKey.Role,
			instKey.JobKey.Name)
	}

	// Currently, instance 0 is always picked..
	return tasks[0].AssignedTask.Task, nil
}

func (r *realisClient) JobUpdateDetails(updateQuery aurora.JobUpdateQuery) (*aurora.Response, error) {

	var resp *aurora.Response
	var err error

	duration := defaultBackoff.Duration
	for i := 0; i < defaultBackoff.Steps; i++ {
		if i != 0 {
			adjusted := duration
			if defaultBackoff.Jitter > 0.0 {
				adjusted = Jitter(duration, defaultBackoff.Jitter)
			}
			fmt.Println(" sleeping for: ", adjusted)
			time.Sleep(adjusted)
			duration = time.Duration(float64(duration) * defaultBackoff.Factor)
		}
		if resp, err = r.client.GetJobUpdateDetails(&updateQuery); err == nil {
			fmt.Println(" resp: ", resp)
			return response.ResponseCodeCheck(resp)
		}
		err1 := r.ReestablishConn()
		if err1 != nil {
			fmt.Println("error in ReestablishConn: ", err1)
		}
	}
	return nil, errors.Wrap(err, "Unable to get job update details")
}

func (r *realisClient) RollbackJobUpdate(key aurora.JobUpdateKey, message string) (*aurora.Response, error) {
	var resp *aurora.Response
	var err error

	duration := defaultBackoff.Duration
	for i := 0; i < defaultBackoff.Steps; i++ {
		if i != 0 {
			adjusted := duration
			if defaultBackoff.Jitter > 0.0 {
				adjusted = Jitter(duration, defaultBackoff.Jitter)
			}
			fmt.Println(" sleeping for: ", adjusted)
			time.Sleep(adjusted)
			duration = time.Duration(float64(duration) * defaultBackoff.Factor)
		}
		if resp, err = r.client.RollbackJobUpdate(&key, message); err == nil {
			fmt.Println(" resp: ", resp)
			return response.ResponseCodeCheck(resp)
		}
		err1 := r.ReestablishConn()
		if err1 != nil {
			fmt.Println("error in ReestablishConn: ", err1)
		}
	}

	return nil, errors.Wrap(err, "Unable to roll back job update")
}
