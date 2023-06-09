package writeflow

import (
	"context"
	"errors"
	"fmt"
	"github.com/samber/lo"
	"github.com/spf13/cast"
	"github.com/zbysir/writeflow/internal/cmd"
	"github.com/zbysir/writeflow/internal/model"
	"github.com/zbysir/writeflow/pkg/schema"
	"sort"
	"sync"
	"time"
)

type WriteFlowCore struct {
	cmds map[string]schema.CMDer
}

func NewWriteFlowCore() *WriteFlowCore {
	return &WriteFlowCore{
		cmds: map[string]schema.CMDer{},
	}
}

func (f *WriteFlowCore) RegisterCmd(key string, cmd schema.CMDer) {
	f.cmds[key] = cmd
}

type NodeInputType = string

const (
	NodeInputAnchor  NodeInputType = "anchor"
	NodeInputLiteral NodeInputType = "literal"
)

type NodeInput struct {
	Key       string
	Type      NodeInputType // anchor, literal
	Literal   interface{}   // 字面量
	NodeId    string        // anchor node id
	OutputKey string
}

type Node struct {
	Id     string
	Cmd    string
	Inputs []NodeInput

	// 只有 for 节点有这个值
	ForItem ForItemNode
}

type ForItemNode struct {
	NodeId    string
	InputKey  string
	OutputKey string // outputKey 可不填，默认等于 inputKey
}

type inputKeysKey struct{}

func WithInputKeys(ctx context.Context, inputKeys []string) context.Context {
	return context.WithValue(ctx, inputKeysKey{}, inputKeys)
}

func GetInputKeys(ctx context.Context) []string {
	if v, ok := ctx.Value(inputKeysKey{}).([]string); ok {
		return v
	}
	return nil
}

type Nodes map[string]Node
type Flow struct {
	Nodes        Nodes // node id -> node
	OutputNodeId string
}

// GetRootNodes Get root nodes that need run
func (d Nodes) GetRootNodes() (nodes []Node) {
	nds := map[string]Node{}
	for k, v := range d {
		nds[k] = v
	}

	for _, v := range d {
		for _, input := range v.Inputs {
			if input.Type == NodeInputAnchor {
				delete(nds, input.NodeId)
			}
		}
		if v.ForItem.NodeId != "" {
			delete(nds, v.ForItem.NodeId)
		}
	}

	// sort for stable
	var keys []string
	for k := range nds {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		nodes = append(nodes, nds[key])
	}
	return nodes
}

func (d *Flow) UsedComponents() (componentType []string) {
	for _, v := range d.Nodes {
		componentType = append(componentType, v.Cmd)
	}
	componentType = lo.Uniq(componentType)

	return componentType
}

func FlowFromModel(m *model.Flow) (*Flow, error) {
	nodes := map[string]Node{}

	for _, node := range m.Graph.Nodes {
		var inputs []NodeInput
		for _, input := range node.Data.InputParams {
			inputs = append(inputs, NodeInput{
				Key:       input.Key,
				Type:      "literal",
				Literal:   node.Data.GetInputValue(input.Key),
				NodeId:    "",
				OutputKey: "",
			})
		}

		for _, input := range node.Data.InputAnchors {
			nodeId, outputKey := node.Data.GetInputAnchorValue(input.Key)
			if nodeId == "" && !input.Optional {
				return nil, fmt.Errorf("input '%v' for node '%v' is not defined", input.Key, node.Id)
			}

			inputs = append(inputs, NodeInput{
				Key:       input.Key,
				Type:      "anchor",
				Literal:   "",
				NodeId:    nodeId,
				OutputKey: outputKey,
			})
		}

		cmd := node.Type
		if node.Data.Source.CmdType == model.NothingCmd {
			cmd = model.NothingCmd
		} else if node.Data.Source.BuiltinCmd != "" {
			cmd = node.Data.Source.BuiltinCmd
		}

		nodes[node.Id] = Node{
			Id:     node.Id,
			Cmd:    cmd,
			Inputs: inputs,
			ForItem: ForItemNode{
				NodeId:    node.Data.ForItem.NodeId,
				InputKey:  node.Data.ForItem.InputKey,
				OutputKey: node.Data.ForItem.OutputKey,
			},
		}
	}
	return &Flow{
		Nodes:        nodes,
		OutputNodeId: m.Graph.GetOutputNodeId(),
	}, nil
}

func (f *WriteFlowCore) ExecFlow(ctx context.Context, flow *Flow, initParams map[string]interface{}) (rsp map[string]interface{}, err error) {
	result := make(chan *model.NodeStatus, len(flow.Nodes))

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		err = f.ExecFlowAsync(ctx, flow, initParams, result)
		if err != nil {
			close(result)
			return
		}
		close(result)
	}()

	for r := range result {
		if r.NodeId == flow.OutputNodeId && r.Status == model.StatusSuccess {
			rsp = r.Result
			break
		}
	}

	return
}

func (f *WriteFlowCore) ExecFlowAsync(ctx context.Context, flow *Flow, initParams map[string]interface{}, results chan *model.NodeStatus) (err error) {
	// use INPUT node to get init params
	f.RegisterCmd("INPUT", cmd.NewFun(func(ctx context.Context, _ map[string]interface{}) (map[string]interface{}, error) {
		return initParams, nil
	}))

	fr := newRunner(f.cmds, flow)
	runNodes := flow.Nodes.GetRootNodes()

	for _, node := range runNodes {
		_, err := fr.ExecNode(ctx, node.Id, nil, func(result model.NodeStatus) {
			results <- &result
		})
		if err != nil {
			return err
		}

		//log.Infof("node: %v, rsp: %+v", node.Id, rsp)
	}

	return
}

type runner struct {
	cmd         map[string]schema.CMDer // id -> cmder
	flowDef     *Flow
	cmdRspCache map[string]map[string]interface{} // nodeId->key->value
	l           sync.RWMutex
}

func (r *runner) getRspCache(nodeId string, key string) (v interface{}, exist bool) {
	r.l.RLock()
	defer r.l.RUnlock()

	if r.cmdRspCache[nodeId] == nil {
		return nil, false
	}
	v = r.cmdRspCache[nodeId][key]
	exist = true
	return
}

func (r *runner) setRspCache(nodeId string, rsp map[string]interface{}) {
	r.l.Lock()
	defer r.l.Unlock()

	r.cmdRspCache[nodeId] = rsp
	return
}

func newRunner(cmd map[string]schema.CMDer, flowDef *Flow) *runner {
	return &runner{cmd: cmd, flowDef: flowDef, cmdRspCache: map[string]map[string]interface{}{}}
}

type ExecNodeError struct {
	Cause  error
	NodeId string
}

func NewExecNodeError(cause error, nodeId string) *ExecNodeError {
	return &ExecNodeError{Cause: cause, NodeId: nodeId}
}

func (e *ExecNodeError) Error() string {
	return fmt.Sprintf("exec node '%s' err: %v", e.NodeId, e.Cause)
}
func (e *ExecNodeError) Unwrap() error {
	return e.Cause
}

var ErrNodeUnreachable = errors.New("node unreachable")

// DGA 可以转为一个表达式。
// 例如：
//
// LangChain({
//   openai: OpenAI({ key: "xx" }).default,
//   prompt: InputStr({ default: "Hi!" }).default
// })
// 这个函数可以通过递归执行依赖的函数（节点）来得到结果。
// 足够简单并且只会执行依赖的函数。
//
// 但要描述 If，和 For 语句就不如 脚本 方便。
//
// Switch:
//
// LangChain({
//   openai: Switch({
//     data: InputStr({default: 'openai'}).default,
//     conditions: [
//        { exp: "data==openai", value: OpenAI({ key: "xx" }).default },
//        { exp: "data==local", value: LocalLLM({ path: "xx" }).default },
//     ],
//   }),
//   prompt: InputStr({ default: "Hi!" }).default
// })
//
// For:
//
// LangChain({
//   openai: OpenAI({ key: "xx" }).default,
//   prompts: For({
//     data: GetList().default,
//     item: AddPrefix({item: <item>, prefix: "Hi: "}).default,
//   })
// })

type Inject map[string]interface{}

func (i Inject) Append(key string, value interface{}) Inject {
	x := map[string]interface{}{}
	for k, v := range i {
		x[k] = v
	}
	x[key] = value
	return x
}

// Switch 和 For 不能使用 Cmd(params map[string]interface{}) map[string]interface{} 实现，而是需要内置。
// Cmd 依赖的是已经处理好的值，而 Switch 和 For 需要依赖懒值（函数），只有当需要的时候才会执行。
// 如果让 Cmd 处理懒值会导致 Cmd 的编写逻辑变得复杂，同时还需要处理函数执行异常，不方便用户编写。
// 而逻辑分支相对固定，可以内置实现。

func (f *runner) ExecNode(ctx context.Context, nodeId string, inject Inject, onNodeRun func(result model.NodeStatus)) (rsp map[string]interface{}, err error) {
	start := time.Now()
	defer func() {
		if onNodeRun != nil {
			if err != nil {
				if errors.Is(err, ErrNodeUnreachable) || err.Error() == ErrNodeUnreachable.Error() {
					onNodeRun(model.NodeStatus{
						NodeId: nodeId,
						Status: model.StatusUnreachable,
						RunAt:  start,
						EndAt:  time.Now(),
					})
					err = nil
				} else {
					onNodeRun(model.NodeStatus{
						NodeId: nodeId,
						Status: model.StatusFailed,
						Error:  err.Error(),
						RunAt:  start,
						EndAt:  time.Now(),
					})
				}
			} else {
				onNodeRun(model.NodeStatus{
					NodeId: nodeId,
					Status: model.StatusSuccess,
					Result: rsp,
					RunAt:  start,
					EndAt:  time.Now(),
				})
			}
		} else {
			if err == ErrNodeUnreachable || err.Error() == ErrNodeUnreachable.Error() {
				err = nil
			}
		}
	}()

	nodeDef := f.flowDef.Nodes[nodeId]

	var calcInput = func(i NodeInput, inject map[string]interface{}) (interface{}, error) {
		var value interface{}
		switch i.Type {
		case NodeInputLiteral:
			value = i.Literal
		case NodeInputAnchor:
			if i.NodeId == "" {
				// 如果节点 id 为空，则说明是非必填字段。
				return nil, nil
			}

			v, ok := f.getRspCache(i.NodeId, i.OutputKey)
			if ok && inject == nil {
				value = v
			} else {
				rsps, err := f.ExecNode(ctx, i.NodeId, inject, onNodeRun)
				if err != nil {
					return nil, err
				}
				value = rsps[i.OutputKey]

				f.setRspCache(i.NodeId, rsps)
			}
		}

		return value, nil
	}

	inputs := nodeDef.Inputs
	//log.Infof("input %v: %+v", nodeId, inputs)
	// switch 和 for 内置实现，不使用 cmd 逻辑。
	switch nodeDef.Cmd {
	case "_switch":
		// get data
		var data interface{}
		for _, input := range inputs {
			if input.Key == "data" {
				data, err = calcInput(input, inject)
				if err != nil {
					return nil, err
				}
			}
		}

		for _, input := range inputs {
			if input.Key == "data" {
				continue
			}

			condition := input.Key

			v, err := LookInterface(data, condition)
			if err != nil {
				return nil, NewExecNodeError(fmt.Errorf("exec condition %s error: %w", condition, err), nodeId)
			}

			if cast.ToBool(v) {
				r, err := calcInput(input, inject)
				if err != nil {
					return nil, err
				}

				return map[string]interface{}{"default": r, "branch": input.Key}, nil
			}
		}

		rsp = map[string]interface{}{"default": nil, "branch": ""}
	case "_for":
		// get data
		var data interface{}
		for _, input := range inputs {
			if input.Key == "data" {
				data, err = calcInput(input, inject)
				if err != nil {
					return nil, err
				}
			}
		}

		outputKey := nodeDef.ForItem.OutputKey
		if outputKey == "" {
			outputKey = nodeDef.ForItem.InputKey
		}

		if nodeDef.ForItem.NodeId == "" {
			return nil, NewExecNodeError(errors.New("the iterated item cannot be empty"), nodeId)
		}

		var rsps []interface{}
		var forError error
		err := ForInterface(data, func(i interface{}) {
			if forError != nil {
				return
			}

			r, err := calcInput(NodeInput{
				Key:       "",
				Type:      NodeInputAnchor,
				Literal:   nil,
				NodeId:    nodeDef.ForItem.NodeId,
				OutputKey: outputKey,
			}, inject.Append(nodeDef.ForItem.InputKey, i))
			if err != nil {
				forError = err
			} else {
				rsps = append(rsps, r)
			}
		})
		if err != nil {
			return nil, NewExecNodeError(fmt.Errorf("for %T error: %w", data, err), nodeId)
		}

		if forError != nil {
			return nil, NewExecNodeError(fmt.Errorf("for %T error: %w", data, forError), nodeId)
		}

		rsp = map[string]interface{}{"default": rsps}
	default:
		dependValue := map[string]interface{}{}
		var inputKeys []string
		for _, i := range inputs {
			inputKeys = append(inputKeys, i.Key)

			r, err := calcInput(i, inject)
			if err != nil {
				return nil, err
			}

			dependValue[i.Key] = r
		}

		cmdName := nodeDef.Cmd
		if cmdName == "" {
			return nil, NewExecNodeError(fmt.Errorf("cmd is not defined"), nodeDef.Id)
		}
		c, ok := f.cmd[cmdName]
		if !ok {
			if cmdName == model.NothingCmd {
				// 如果不需要执行任何命令，则直接返回 input
				return dependValue, nil
			}
			return nil, NewExecNodeError(fmt.Errorf("cmd '%s' not found", cmdName), nodeDef.Id)
		}

		for k, v := range inject {
			dependValue[k] = v
		}

		// 只有自定义 cmd 才需要报告 running 状态，特殊的 _for, _switch 不需要。
		if onNodeRun != nil {
			onNodeRun(model.NodeStatus{
				NodeId: nodeId,
				Status: model.StatusRunning,
				Error:  "",
				Result: nil,
				RunAt:  start,
				EndAt:  time.Time{},
			})
		}

		rsp, err = c.Exec(WithInputKeys(ctx, inputKeys), dependValue)
		if err != nil {
			return nil, NewExecNodeError(err, nodeDef.Id)
		}
	}

	//log.Printf("dependValue: %+v", dependValue)

	return rsp, err
}
