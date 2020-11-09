package task

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func emptyScheduler() {
	ClearJobs()
}
func TestOnceJob(t *testing.T) {
	defer emptyScheduler()
	name := "test_job"
	job := Once(name, func(a, b int) int { return a + b }, 10, 20)
	assert.True(t, job.IsRunnable(time.Now()))
	assert.Equal(t, 1, defaultScheduler.JobCount())

	job.Delay(2 * time.Second)
	assert.False(t, job.IsRunnable(time.Now()))
	time.Sleep(2 * time.Second)
	assert.True(t, job.IsRunnable(time.Now()))

	job.Run(time.Now())
	assert.False(t, job.IsRunnable(time.Now()))
	jobStats := job.Stats()
	assert.Len(t, jobStats, 1)
}

func TestOnceJobCoordinate(t *testing.T) {
	defer emptyScheduler()
	name := "test_once_job_coordinate"
	coordinator := NewCoordinatorFromRedis("coordinator1", "localhost:6379")

	scheduler1 := NewScheduler(10)
	scheduler2 := NewScheduler(10)
	function := func(a int) int { return a }

	job1 := scheduler1.AddRunOnceJob(name, function, 1).SetCoordinate(coordinator)
	job2 := scheduler2.AddRunOnceJob(name, function, 1).SetCoordinate(coordinator)

	currentTime := time.Now()
	assert.True(t, job1.IsRunnable(currentTime))
	assert.True(t, job2.IsRunnable(currentTime))
	job1.Run(currentTime)
	assert.False(t, job1.IsRunnable(currentTime))
	assert.False(t, job2.IsRunnable(currentTime))

}

func TestPeroidicJob(t *testing.T) {
	defer emptyScheduler()
	name := "test_job"
	job := Periodic(name, func(a, b int) int { return a + b }, 10, 20)
	runnable := job.IsRunnable(time.Now())
	assert.False(t, runnable)

}

func TestPeroidicJobTimeZone(t *testing.T) {
	defer emptyScheduler()
	// loc1 is UTC+8
	loc1, _ := time.LoadLocation("Asia/Shanghai")
	// loc2 is UTC
	loc2, _ := time.LoadLocation("UTC")
	// loc3 is UTC-6
	loc3 := time.FixedZone("UTC-6", -6*60*60)

	name := "test_job"
	job := Periodic(name, func(a, b int) int { return a + b }, 10, 20)
	job.EveryDays(1).SetTimeZone(loc1).AtHourInDay(10, 0, 0)

	t1 := time.Date(2019, time.November, 2, 10, 0, 0, 0, loc1)
	assert.True(t, job.IsRunnable(t1))

	t2 := time.Date(2019, time.November, 2, 10, 0, 0, 0, loc2)
	assert.False(t, job.IsRunnable(t2))

	t3 := time.Date(2019, time.November, 2, 2, 0, 0, 0, loc2)
	assert.True(t, job.IsRunnable(t3))

	t4 := time.Date(2019, time.November, 1, 20, 0, 0, 0, loc3)
	assert.True(t, job.IsRunnable(t4))
}

func TestPeroidicJobNoAtTime(t *testing.T) {
	defer emptyScheduler()
	name := "test_job"
	job := Periodic(name, func(a, b int) int { return a + b }, 10, 20)
	job.EveryMinutes(1)

	executeTime := time.Date(2020, time.November, 2, 10, 10, 30, 0, time.Local)
	assert.False(t, job.IsRunnable(executeTime))

	executeTime = time.Date(2020, time.November, 2, 10, 10, 0, 0, time.Local)
	assert.True(t, job.IsRunnable(executeTime))
	job.Run(executeTime)
	assert.False(t, job.IsRunnable(executeTime))

	executeTime = time.Date(2020, time.November, 2, 10, 11, 0, 0, time.Local)
	assert.True(t, job.IsRunnable(executeTime))
}

func TestPeroidicJobEveryDay(t *testing.T) {
	defer emptyScheduler()
	name := "test_job"
	job := Periodic(name, func(a, b int) int { return a + b }, 10, 20)
	hour := 10
	minute := 20
	second := 30
	job.EveryDays(2).AtHourInDay(hour, minute, second)

	executeTime := time.Date(2020, time.November, 2, 10, 10, 30, 0, time.Local)
	assert.False(t, job.IsRunnable(executeTime))

	executeTime = time.Date(2020, time.November, 2, hour, minute, second, 50, time.Local)
	assert.True(t, job.IsRunnable(executeTime))
	job.Run(executeTime)
	assert.False(t, job.IsRunnable(executeTime))

	executeTime = executeTime.Add(1 * 24 * time.Hour)
	assert.False(t, job.IsRunnable(executeTime))

	executeTime = executeTime.Add(1 * 24 * time.Hour)
	assert.True(t, job.IsRunnable(executeTime))
	job.Run(executeTime)
	assert.False(t, job.IsRunnable(executeTime))
}

func TestPeroidicJobEveryHour(t *testing.T) {
	defer emptyScheduler()
	name := "test_job"
	job := Periodic(name, func(a, b int) int { return a + b }, 10, 20)
	minute := 20
	second := 30
	job.EveryHours(2).AtMinuteInHour(minute, second)

	executeTime := time.Date(2020, time.November, 2, 1, minute, second+10, 50, time.Local)
	assert.False(t, job.IsRunnable(executeTime))

	executeTime = time.Date(2020, time.November, 2, 1, minute, second, 50, time.Local)
	assert.True(t, job.IsRunnable(executeTime))
	job.Run(executeTime)
	assert.False(t, job.IsRunnable(executeTime))

	executeTime = executeTime.Add(1 * time.Hour)
	assert.False(t, job.IsRunnable(executeTime))

	executeTime = executeTime.Add(1 * time.Hour)
	assert.True(t, job.IsRunnable(executeTime))
	job.Run(executeTime)
	assert.False(t, job.IsRunnable(executeTime))
}

func TestPeroidicJobEveryMinute(t *testing.T) {
	defer emptyScheduler()
	name := "test_job"
	job := Periodic(name, func(a, b int) int { return a + b }, 10, 20)
	second := 30
	job.EveryMinutes(2).AtSecondInMinute(second)

	executeTime := time.Date(2020, time.November, 2, 1, 0, second+10, 50, time.Local)
	assert.False(t, job.IsRunnable(executeTime))

	executeTime = time.Date(2020, time.November, 2, 1, 0, second, 50, time.Local)
	assert.True(t, job.IsRunnable(executeTime))
	job.Run(executeTime)
	assert.False(t, job.IsRunnable(executeTime))

	executeTime = executeTime.Add(1 * time.Minute)
	assert.False(t, job.IsRunnable(executeTime))

	executeTime = executeTime.Add(1 * time.Minute)
	assert.True(t, job.IsRunnable(executeTime))
	job.Run(executeTime)
	assert.False(t, job.IsRunnable(executeTime))
}

func TestPeroidicJobEveryWeekday(t *testing.T) {
	defer emptyScheduler()
	name := "test_job"
	job := Periodic(name, func(a, b int) int { return a + b }, 10, 20)
	currentTime := time.Now()
	hour := currentTime.Hour()
	minute := int(math.Mod(float64(currentTime.Minute())+10, 60))
	second := currentTime.Second()
	job.EveryMondays(2).AtHourInDay(hour, minute, second)

	// a Sunday
	executeTime := time.Date(2020, time.November, 1, hour, minute, second, 0, time.Local)
	assert.False(t, job.IsRunnable(executeTime))

	// a Monday
	executeTime = time.Date(2020, time.November, 2, hour, minute, second, 0, time.Local)
	assert.True(t, job.IsRunnable(executeTime))
	job.Run(executeTime)
	assert.False(t, job.IsRunnable(executeTime))

	// next monday
	executeTime = executeTime.Add(7 * 24 * time.Hour)
	assert.False(t, job.IsRunnable(executeTime))

	// next next monday
	executeTime = executeTime.Add(7 * 24 * time.Hour)
	assert.True(t, job.IsRunnable(executeTime))
	job.Run(executeTime)
	assert.False(t, job.IsRunnable(executeTime))
}

func TestPeriodicJobCoordinator(t *testing.T) {
	defer emptyScheduler()
	coordinator := NewCoordinatorFromRedis("coordinator1", "localhost:6379")

	scheduler1 := NewScheduler(10)
	scheduler2 := NewScheduler(10)
	sum := 0
	function := func(a int) { sum = sum + a }

	name := "test_job"
	job1 := scheduler1.AddPeriodicJob(name, function, 1).EverySeconds(5).SetCoordinate(coordinator)
	job2 := scheduler2.AddPeriodicJob(name, function, 1).EverySeconds(5).SetCoordinate(coordinator)

	currentTime := time.Now()
	assert.True(t, job1.IsRunnable(currentTime))
	assert.True(t, job2.IsRunnable(currentTime))
	job1.Run(currentTime)
	assert.True(t, currentTime.Truncate(time.Second).Equal(job1.scheduledTime))
	assert.False(t, job1.IsRunnable(currentTime))
	assert.False(t, job2.IsRunnable(currentTime))
	assert.True(t, currentTime.Truncate(time.Second).Equal(job2.scheduledTime))

	time.Sleep(5 * time.Second)
	currentTime = time.Now()
	assert.True(t, job1.IsRunnable(currentTime))
	assert.True(t, job2.IsRunnable(currentTime))
	job2.Run(currentTime)
	assert.False(t, job1.IsRunnable(currentTime))
	assert.False(t, job2.IsRunnable(currentTime))
	assert.True(t, currentTime.Truncate(time.Second).Equal(job1.scheduledTime))
	assert.True(t, currentTime.Truncate(time.Second).Equal(job2.scheduledTime))
}
