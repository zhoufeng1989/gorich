package task

import (
	"context"
	"errors"
	"fmt"
	"log"
	"reflect"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
)

const (
	defaultConcurrentWorkerCount = 100
	defaultQueueSize             = 500
	maxStatsCount                = 100
)

var defaultScheduler = NewScheduler(defaultQueueSize, defaultConcurrentWorkerCount)

type Scheduler struct {
	jobs                  map[string]Job
	concurrentWorkerCount int
	queue                 chan Job
}

func NewScheduler(queueSize, workerCount int) Scheduler {
	return Scheduler{
		jobs:                  make(map[string]Job, 0),
		concurrentWorkerCount: workerCount,
		queue:                 make(chan Job, queueSize),
	}
}

func (scheduler *Scheduler) jobCount() int {
	return len(scheduler.jobs)
}

func (scheduler *Scheduler) AddPeriodicJob(name string, function interface{}, params ...interface{}) *PeriodicJob {
	job := &PeriodicJob{
		name:     name,
		function: function,
		params:   params,
		cron:     &Cron{timezone: time.Local},
	}
	scheduler.jobs[name] = job
	return job
}

func (scheduler *Scheduler) AddRunOnceJob(name string, function interface{}, params ...interface{}) *OnceJob {
	job := NewOnceJob(name, function, params)
	scheduler.jobs[name] = job
	return job
}

func (scheduler *Scheduler) getRunnableJobs(t time.Time) []Job {
	runnableJobs := []Job{}
	for _, job := range scheduler.jobs {
		if job.IsRunnable(t) {
			runnableJobs = append(runnableJobs, job)
		}
	}
	return runnableJobs
}

func (scheduler *Scheduler) Start() {
	ticker := time.NewTicker(1 * time.Second)
	for {
		select {
		case tickerTime := <-ticker.C:
			scheduler.runJobs(tickerTime)
		}
	}
}

func (scheduler *Scheduler) runJobs(t time.Time) {
	runnableJobs := scheduler.getRunnableJobs(t)
	for _, job := range runnableJobs {
		job.ScheduledAt(t)
		go job.Run(t)
	}
}

func (scheduler *Scheduler) JobStats() map[string][]JobStat {
	jobStats := make(map[string][]JobStat, len(scheduler.jobs))
	for name, job := range scheduler.jobs {
		jobStats[name] = job.Stats()
	}
	return jobStats
}

type Coordinator struct {
	name        string
	redisClient *redis.Client
}

func NewCoordinator(name string, address string) *Coordinator {
	redisClient := redis.NewClient(&redis.Options{Addr: address})
	return &Coordinator{name: name, redisClient: redisClient}
}

func (coordinator Coordinator) Coordinate(name string, scheduledTime, nextScheduledTime time.Time) {
	key := fmt.Sprintf("task:%s:%s", coordinator.name, name)
	scheduledTs := scheduledTime.Truncate(time.Second).Unix()
	nextScheduledTs := nextScheduledTime.Truncate(time.Second).Unix()
	fmt.Printf("scheduled_ts:%d, next_scheduled_ts:%d\n", scheduledTs, nextScheduledTs)
	script := `
		local key = KEYS[1]
		local current_scheduled_ts = ARGV[1]
		local next_scheduled_ts = ARGV[2]
		local table = redis.call("HGETALL", key) 
		local can_be_scheduled = 1
		local scheduled_time = current_scheduled_ts
		if next(table) == nil then 
			redis.call("HMSET", key, "current_scheduled_ts", current_scheduled_ts, "next_scheduled_ts", next_scheduled_ts)
			redis.call("EXPIREAT", key, next_scheduled_ts)
		else
			local key1 = table[1]
			local value1 = table[2]
			local key2 = table[3]
			local value2 = table[4]
			local scheduled_info_table = {}
			scheduled_info_table[key1] = value1
			scheduled_info_table[key2] = value2
			local recorded_next_ts = tonumber(scheduled_info_table["next_scheduled_ts"])
			if recorded_next_ts <= tonumber(current_scheduled_ts) then
				redis.call("HMSET", key, "current_scheduled_ts", current_scheduled_ts, "next_scheduled_ts", next_scheduled_ts)
				redis.call("EXPIREAT", key, next_scheduled_ts)
			else
				scheduled_time = scheduled_info_table["current_scheduled_ts"]
				can_be_scheduled = 0
			end
		end
		return {can_be_scheduled, scheduled_time}
	`
	redisScript := redis.NewScript(script)
	result, err := redisScript.Run(
		context.Background(),
		coordinator.redisClient, []string{key},
		scheduledTs,
		nextScheduledTs).Result()

	canBeScheduled := result.([]interface{})[0].(int64)
	scheduledAt := result.([]interface{})[1].(string)
	fmt.Println(result, canBeScheduled, scheduledAt, err, err == nil)
}

func (job *PeriodicJob) Coordinate(coordinator *Coordinator) *PeriodicJob {
	job.coordinator = coordinator
	return job
}

// func test() {
// 	key := "task:coname:jobname"
// 	scheduledTime := time.Now().Truncate(time.Second)
// 	nextScheduledTime := time.Now()
// 	x := redis.hgetall(key)
// 	if x && x.next_scheduled_time <= scheduledTime || !x {
// 		redis.hmset(key, scheduledTime, nextScheduledTime)
// 		redis.expireAt(key, nextScheduledTime)
// 	}
// 	return scheduledTime
// 	setScheduledTime(scheduledTime)
// }

func Once(name string, function interface{}, params ...interface{}) *OnceJob {
	return defaultScheduler.AddRunOnceJob(name, function, params...)
}

func Periodic(name string, function interface{}, params ...interface{}) *PeriodicJob {
	return defaultScheduler.AddPeriodicJob(name, function, params...)
}

func Start() {
	defaultScheduler.Start()
}

func JobStats() map[string][]JobStat {
	return defaultScheduler.JobStats()
}

type JobStat struct {
	IsSuccess     bool
	Err           error
	RunDuration   time.Duration
	ScheduledTime time.Time
}

func (stat JobStat) ToMap() map[string]interface{} {
	return map[string]interface{}{
		"success":       stat.IsSuccess,
		"error":         stat.Err,
		"run_duration":  stat.RunDuration,
		"scheduledTime": stat.ScheduledTime,
	}
}

type Job interface {
	Name() string
	IsRunnable(time.Time) bool
	ScheduledAt(time.Time)
	Run(time.Time)
	Stats() []JobStat
}

type OnceJob struct {
	name          string
	function      interface{}
	params        []interface{}
	delay         time.Duration
	runnable      bool
	timer         *time.Timer
	scheduled     bool
	scheduledTime time.Time
	jobStats      []JobStat
	jobStatLock   sync.Mutex
}

func NewOnceJob(name string, function interface{}, params ...interface{}) *OnceJob {
	job := &OnceJob{
		name:     name,
		function: function,
		params:   params,
		delay:    0,
		runnable: true,
	}
	return job
}

func (job *OnceJob) Name() string {
	return job.name
}

func (job *OnceJob) Stats() []JobStat {
	return job.jobStats
}

func (job *OnceJob) Delay(delay time.Duration) *OnceJob {
	job.delay = delay
	job.runnable = false
	if job.timer != nil {
		job.timer.Stop()
	}
	go job.waitUntilRunnable()
	return job
}

func (job *OnceJob) waitUntilRunnable() {
	job.timer = time.NewTimer(job.delay)
	<-job.timer.C
	job.runnable = true
}

func (job *OnceJob) IsRunnable(t time.Time) bool {
	return job.runnable && !job.scheduled
}

func (job *OnceJob) ScheduledAt(t time.Time) {
	job.scheduledTime = t
	job.scheduled = true
}

func (job *OnceJob) Run(t time.Time) {
	log.Printf("run job: %s\n", job.name)
	startTime := time.Now()
	stat := runJobFunctionAndGetJobStat(job.function, job.params)
	stat.RunDuration = time.Now().Sub(startTime)
	stat.ScheduledTime = t
	job.addStat(stat)
}

func (job *OnceJob) addStat(stat JobStat) {
	job.jobStatLock.Lock()
	defer job.jobStatLock.Unlock()
	job.jobStats = append(job.jobStats, stat)
	if len(job.jobStats) > maxStatsCount {
		job.jobStats = job.jobStats[len(job.jobStats)-maxStatsCount:]
	}
}

type PeriodicJob struct {
	name          string
	function      interface{}
	params        []interface{}
	cron          *Cron
	scheduledTime time.Time
	jobStats      []JobStat
	jobStatLock   sync.Mutex
	coordinator   *Coordinator
}

func (job *PeriodicJob) Name() string {
	return job.name
}

func (job *PeriodicJob) Stats() []JobStat {
	return job.jobStats
}

func (job *PeriodicJob) IsRunnable(t time.Time) bool {
	at := getAtTime(job.cron.intervalType, t)
	var isRunnable bool
	// scheduledTime.IsZero == true if the job has not been sheduled yet.
	if job.scheduledTime.IsZero() {
		if (job.cron.at == 0) || (at == job.cron.at) {
			if job.cron.intervalType == intervalWeek {
				if job.cron.weekDay == t.Weekday() {
					isRunnable = true
				} else {
					isRunnable = false
				}
			} else {
				isRunnable = true
			}
		} else {
			isRunnable = false
		}
	} else {
		roundScheduledTime := job.scheduledTime.Truncate(time.Second)
		roundCurrentTime := t.Truncate(time.Second)
		if !roundCurrentTime.Before(roundScheduledTime.Add(job.cron.IntervalDuration())) {
			isRunnable = true
		} else {
			isRunnable = false
		}
	}
	return isRunnable
}

func (job *PeriodicJob) ScheduledAt(t time.Time) {
	job.scheduledTime = t
}

// func (job *PeriodicJob) NextRunTime(tickTime time.Time) time.Time {
// 	if job.scheduledTime.IsZero() {
// 		return tickTime.Truncate(time.Second)
// 	}
// 	return job.scheduledTime.Add(job.cron.IntervalDuration()).Truncate(time.Second)
// }

func (job *PeriodicJob) EverySeconds(second int) *PeriodicJob {
	job.cron.intervalType = intervalSecond
	job.cron.interval = second
	return job
}

func (job *PeriodicJob) EveryMinutes(minute int) *PeriodicJob {
	job.cron.intervalType = intervalMinute
	job.cron.interval = minute
	return job
}

func (job *PeriodicJob) EveryHours(hour int) *PeriodicJob {
	job.cron.intervalType = intervalHour
	job.cron.interval = hour
	return job
}

func (job *PeriodicJob) EveryDays(day int) *PeriodicJob {
	job.cron.intervalType = intervalDay
	job.cron.interval = day
	return job
}

func (job *PeriodicJob) EveryMondays(week int) *PeriodicJob {
	job.cron.intervalType = intervalWeek
	job.cron.interval = week
	job.cron.weekDay = time.Monday
	return job
}

func (job *PeriodicJob) EveryTuesdays(week int) *PeriodicJob {
	job.cron.intervalType = intervalWeek
	job.cron.interval = week
	job.cron.weekDay = time.Tuesday
	return job
}

func (job *PeriodicJob) EveryWednesdays(week int) *PeriodicJob {
	job.cron.intervalType = intervalWeek
	job.cron.interval = week
	job.cron.weekDay = time.Wednesday
	return job
}

func (job *PeriodicJob) EveryThursdays(week int) *PeriodicJob {
	job.cron.intervalType = intervalWeek
	job.cron.interval = week
	job.cron.weekDay = time.Thursday
	return job
}

func (job *PeriodicJob) EveryFridays(week int) *PeriodicJob {
	job.cron.intervalType = intervalWeek
	job.cron.interval = week
	job.cron.weekDay = time.Friday
	return job
}

func (job *PeriodicJob) EverySaturdays(week int) *PeriodicJob {
	job.cron.intervalType = intervalWeek
	job.cron.interval = week
	job.cron.weekDay = time.Saturday
	return job
}

func (job *PeriodicJob) EverySundays(week int) *PeriodicJob {
	job.cron.intervalType = intervalWeek
	job.cron.interval = week
	job.cron.weekDay = time.Sunday
	return job
}

func (job *PeriodicJob) AtHourInDay(hour, minute, second int) (*PeriodicJob, error) {
	if err := job.cron.AtHourInDay(hour, minute, second); err != nil {
		return nil, err
	}
	return job, nil
}

func (job *PeriodicJob) AtMinuteInHour(minute, second int) (*PeriodicJob, error) {
	if err := job.cron.AtMinuteInHour(minute, second); err != nil {
		return nil, err
	}
	return job, nil
}

func (job *PeriodicJob) AtSecondInMinute(second int) (*PeriodicJob, error) {
	if err := job.cron.AtSecondInMinute(second); err != nil {
		return nil, err
	}
	return job, nil
}

func getAtTime(intervalType IntervalType, t time.Time) time.Duration {
	var at time.Duration
	switch intervalType {
	case intervalWeek:
		at = time.Duration(t.Hour())*time.Hour +
			time.Duration(t.Minute())*time.Minute +
			time.Duration(t.Second())*time.Second
	case intervalDay:
		at = time.Duration(t.Hour())*time.Hour +
			time.Duration(t.Minute())*time.Minute +
			time.Duration(t.Second())*time.Second
	case intervalHour:
		at = time.Duration(t.Minute())*time.Minute +
			time.Duration(t.Second())*time.Second
	case intervalMinute:
		at = time.Duration(t.Second()) * time.Second
	case intervalSecond:
		at = time.Duration(0)
	}
	return at
}

func (job *PeriodicJob) Run(t time.Time) {
	log.Printf("run job: %s\n", job.name)
	startTime := time.Now()
	stat := runJobFunctionAndGetJobStat(job.function, job.params)
	stat.RunDuration = time.Now().Sub(startTime)
	stat.ScheduledTime = t
	job.addStat(stat)
}

func interfaceToError(i interface{}) error {
	var err error
	switch v := i.(type) {
	case error:
		err = v
	case nil:
		err = nil
	default:
		err = fmt.Errorf("%+v", v)
	}
	return err
}

func (job *PeriodicJob) addStat(stat JobStat) {
	job.jobStatLock.Lock()
	defer job.jobStatLock.Unlock()
	job.jobStats = append(job.jobStats, stat)
	if len(job.jobStats) > maxStatsCount {
		job.jobStats = job.jobStats[len(job.jobStats)-maxStatsCount:]
	}
}

var ErrNotFunctionType = errors.New("job's function is not function type")
var ErrFunctionArityNotMatch = errors.New("job's function arity does not match given parameters")

func runJobFunctionAndGetJobStat(function interface{}, params []interface{}) JobStat {
	stat := JobStat{
		IsSuccess: true,
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			stat.IsSuccess = false
			stat.Err = interfaceToError(recovered)
		}
	}()

	if reflect.TypeOf(function).Kind() != reflect.Func {
		stat.IsSuccess = false
		stat.Err = ErrNotFunctionType
	}
	f := reflect.ValueOf(function)
	if len(params) != f.Type().NumIn() {
		stat.IsSuccess = false
		stat.Err = ErrFunctionArityNotMatch
	}
	in := make([]reflect.Value, len(params))
	for k, param := range params {
		in[k] = reflect.ValueOf(param)
	}
	jobResults := f.Call(in)
	for _, result := range jobResults {
		if err, ok := result.Interface().(error); ok {
			stat.IsSuccess = false
			stat.Err = err
		}
	}
	return stat
}

type IntervalType string

const (
	intervalSecond IntervalType = "second"
	intervalMinute IntervalType = "minute"
	intervalHour   IntervalType = "hour"
	intervalDay    IntervalType = "day"
	intervalWeek   IntervalType = "week"
	intervalMonth  IntervalType = "month"
)

type Cron struct {
	interval     int
	intervalType IntervalType
	weekDay      time.Weekday
	at           time.Duration
	timezone     *time.Location
}

func newCron(interval int, intervalType IntervalType, at time.Duration, timezone *time.Location) *Cron {
	// interval less or euqual to 0 will be set to 1.
	if interval <= 0 {
		interval = 1
	}
	return &Cron{
		interval:     interval,
		intervalType: intervalType,
		at:           at,
		timezone:     timezone,
	}
}

func EverySeconds(second int) *Cron {
	return newCron(second, intervalSecond, 0, time.Local)
}

func EveryMinutes(minute int) *Cron {
	return newCron(minute, intervalMinute, 0, time.Local)
}

func EveryHours(hour int) *Cron {
	return newCron(hour, intervalHour, 0, time.Local)
}

func EveryDays(day int) *Cron {
	return newCron(day, intervalDay, 0, time.Local)
}

var ErrTimeRange = errors.New("time range is invalid")

func (cron *Cron) AtHourInDay(hour, minute, second int) error {
	if !isValidHour(hour) || !isValidMinute(minute) && !isValidSecond(second) {
		return ErrTimeRange
	}
	cron.at = time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute + time.Duration(second)*time.Second
	return nil
}

func (cron *Cron) AtMinuteInHour(minute, second int) error {
	if !isValidMinute(minute) || !isValidSecond(second) {
		return ErrTimeRange
	}
	cron.at = time.Duration(minute)*time.Minute + time.Duration(second)*time.Second
	return nil
}

func (cron *Cron) AtSecondInMinute(second int) error {
	if !isValidSecond(second) {
		return ErrTimeRange
	}
	cron.at = time.Duration(second) * time.Second
	return nil
}

func (cron *Cron) IntervalDuration() time.Duration {
	var duration time.Duration
	switch cron.intervalType {
	case intervalWeek:
		duration = time.Duration(cron.interval) * 7 * 24 * time.Hour
	case intervalDay:
		duration = time.Duration(cron.interval) * 24 * time.Hour
	case intervalHour:
		duration = time.Duration(cron.interval) * time.Hour
	case intervalMinute:
		duration = time.Duration(cron.interval) * time.Minute
	case intervalSecond:
		duration = time.Duration(cron.interval) * time.Second
	}
	return duration
}

func isValidHour(hour int) bool {
	return (hour > 0) && (hour < 24)
}

func isValidMinute(minute int) bool {
	return (minute > 0) && (minute < 60)
}

func isValidSecond(second int) bool {
	return (second > 0) && (second < 60)
}
