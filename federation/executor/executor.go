package executor

import (
	"sync"

	"github.com/n9te9/federation-gateway/federation/planner"
)

type Executor interface {
	Execute(plan *planner.Plan) error
}

type executor struct {
	QueryBuilder
}

func NewExecutor() *executor {
	return &executor{}
}

func (e *executor) Execute(plan *planner.Plan) error {
	wg := sync.WaitGroup{}
	for _, step := range plan.Steps {
		wg.Add(1)
		go func(step *planner.Step) {
			e.waitDependStepEnded(plan, step)

			step.Run()
			if step.Err != nil {
				step.Fail()
			} else {
				step.Complete()
			}
			close(step.Done)

			wg.Done()
		}(step)
	}

	wg.Wait()
	return nil
}

func (e *executor) waitDependStepEnded(plan *planner.Plan, step *planner.Step) {
	for _, dependStepID := range step.DependsOn {
		dependsStep := plan.GetStepByID(dependStepID)
		<-dependsStep.Done
	}
}
