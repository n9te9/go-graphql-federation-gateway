package executor

import (
	"sync"

	"github.com/n9te9/federation-gateway/federation/planner"
)

type Executor interface {
	Execute(plan *planner.Plan) error
}

type executor struct {
	mutex *sync.Mutex
}

func NewExecutor() *executor {
	return &executor{
		mutex: &sync.Mutex{},
	}
}

func (e *executor) Execute(plan *planner.Plan) error {
	for _, step := range plan.Steps {
		go func(step *planner.Step) {
			e.waitDependStepEnded(plan, step)

			e.mutex.Lock()
			step.Run()
			step.Complete()

			defer e.mutex.Unlock()
		}(step)
	}
	return nil
}

func (e *executor) waitDependStepEnded(plan *planner.Plan, step *planner.Step) {
	for {
		ended := true

		for _, dependStepID := range step.DependsOn {
			dependsStep := plan.GetStepByID(dependStepID)
			if dependsStep.Status == planner.Running {
				ended = false
			}
		}

		if ended {
			break
		}
	}
}
