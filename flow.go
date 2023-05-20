package writeflow

import (
	"context"
	"fmt"
	"github.com/zbysir/writeflow/internal/cmd"
	"github.com/zbysir/writeflow/pkg/schema"
	"gopkg.in/yaml.v2"
	"strings"
)

type WriteFlow struct {
	cmds map[string]*Component
}

func NewShelFlow() *WriteFlow {
	return &WriteFlow{
		cmds: map[string]*Component{},
	}
}

func (f *WriteFlow) RegisterComponent(cmd *Component) {
	key := cmd.Schema.Key
	f.cmds[key] = cmd
}

// 所有的依赖可以并行计算。
// 这是通过代码逻辑不好描述的
//
// appendName-1:
//   cmd: appendName
//   input:
//     - _args[0]
//     - _args[1]
// hello:
//   cmd: hello
//   input:
//     - appendName-1[0]
//
// END:
//   input:
//     - hello[0]

// flow: 流程定义
// job: flow 由 多个 job 组成
// cmd: job 可以调用 cmd

type JobInput struct {
	// _args[0]
	JobName   string
	RespIndex string
	Key       string
	// {a: _args[1]}
	//Object map[string]JobInput
}

type JobDef struct {
	Name   string
	Cmd    string
	Inputs []JobInput
}
type FlowDef struct {
	Jobs map[string]JobDef
}

// SpanInterface 特殊语法，返回值
type SpanInterface []interface{}

type YFlow struct {
	Version string          `yaml:"version"`
	Flow    map[string]YJob `yaml:"flow"`
}

type YJob struct {
	Cmd     string                 `yaml:"cmd"`
	Inputs  map[string]interface{} `yaml:"inputs"`
	Depends []string               `yaml:"depends"`
}

func (f *YFlow) ToFlowDef() FlowDef {
	jobs := map[string]JobDef{}
	for name, v := range f.Flow {
		jobs[name] = v.ToJobDef(name)
	}

	return FlowDef{Jobs: jobs}
}

func (j *YJob) ToJobDef(name string) JobDef {
	var inputs []JobInput
	for key, item := range j.Inputs {
		switch item := item.(type) {
		case string:
			// _args[1]
			ss := strings.Split(item, "[")
			taskName := ""
			var respIndex string
			if len(ss) == 2 {
				taskName = ss[0]
				respIndex = ss[1][0 : len(ss[1])-1]
			} else {
				taskName = ss[0]
				respIndex = "default" // -1 表示就当成数值传递
			}

			inputs = append(inputs, JobInput{
				JobName:   taskName,
				RespIndex: respIndex,
				Key:       key,
			})
		case map[string]interface{}:
			// {name: args[0]}
			// TODO object
		}
	}

	return JobDef{
		Name:   name,
		Cmd:    j.Cmd,
		Inputs: inputs,
	}
}

func (f *WriteFlow) parseFlow(flow string) (FlowDef, error) {
	var flowDefI YFlow
	err := yaml.Unmarshal([]byte(flow), &flowDefI)
	if err != nil {
		return FlowDef{}, fmt.Errorf("unmarshal flow err: %v", err)
	}
	def := flowDefI.ToFlowDef()

	return def, nil
}

func (f *WriteFlow) ExecFlow(ctx context.Context, flow string, params map[string]interface{}) (rsp map[string]interface{}, err error) {
	f.RegisterComponent(NewComponent(cmd.NewFun(func(ctx context.Context, _ map[string]interface{}) (map[string]interface{}, error) {
		return params, nil
	}), cmd.Schema{
		Key: "INPUT",
	}))
	def, err := f.parseFlow(flow)
	if err != nil {
		return nil, err
	}

	cmds := map[string]schema.CMDer{}
	for k, v := range f.cmds {
		cmds[k] = v.Cmder
	}

	fr := FlowRun{
		flowDef: def,
		cmdRsp:  map[string]map[string]interface{}{},
		cmd:     cmds,
	}
	rsp, err = fr.ExecJob(ctx, "END")
	if err != nil {
		return
	}

	return
}

func (f *WriteFlow) GetCMDs(ctx context.Context, names []string) (rsp []cmd.Schema, err error) {
	for _, cmd := range f.cmds {
		rsp = append(rsp, cmd.Schema)
	}

	return
}

type FlowRun struct {
	cmd     map[string]schema.CMDer
	flowDef FlowDef
	cmdRsp  map[string]map[string]interface{}
}

func (f *FlowRun) ExecJob(ctx context.Context, jobName string) (rsp map[string]interface{}, err error) {
	jobDef := f.flowDef.Jobs[jobName]
	inputs := jobDef.Inputs

	//log.Printf("exec: %s, inputs: %v", jobName, inputs)
	dependValue := map[string]interface{}{}
	for _, i := range inputs {
		var rsp interface{}
		if f.cmdRsp[i.JobName] != nil {
			//log.Printf("i.JobName %v: %+v", i.JobName, f.cmdRsp[i.JobName])
			// cache
			rsp = f.cmdRsp[i.JobName][i.RespIndex]
		} else {
			rsps, err := f.ExecJob(ctx, i.JobName)
			if err != nil {
				return nil, fmt.Errorf("exec task '%s' err: %v", i.JobName, err)
			}

			rsp = rsps[i.RespIndex]

			f.cmdRsp[i.JobName] = rsps
		}

		dependValue[i.Key] = rsp
	}

	//log.Printf("dependValue: %+v", dependValue)
	cmd := jobDef.Cmd
	if cmd == "" {
		cmd = jobName
	}
	c, ok := f.cmd[cmd]
	if ok {
		rsp, err := c.Exec(ctx, dependValue)
		//rsp, err := execFunc(ctx, c, dependValue)
		//if err != nil {
		//	return nil, fmt.Errorf("exec task '%s' err: %w", jobName, err)
		//}

		return rsp, err
	} else {
		return dependValue, nil
	}

}
