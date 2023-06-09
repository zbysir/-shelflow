package writeflow

import (
	"github.com/zbysir/writeflow/internal/model"
	"testing"
)

func TestComponentFromModel(t *testing.T) {
	cmd, m, err := ComponentFromModel(&model.Component{
		Type: "demo",
		Data: model.ComponentData{
			//Id:          "",
			Source: model.ComponentSource{
				CmdType: model.GoScriptCmd,
				GoScript: model.ComponentGoScript{
					InputKey: "script",
				},
			},
			InputAnchors: []model.NodeInputAnchor{
				{
					Name: map[string]string{
						"zh-CN": "姓名",
					},
					Key:  "name",
					Type: "string",
					List: false,
				},
				{
					Name: map[string]string{
						"zh-CN": "年龄",
					},
					Key:  "age",
					Type: "int",
					List: false,
				},
			},
			InputParams: []model.NodeInputParam{
				{
					Name:        nil,
					Key:         "script",
					Type:        "string",
					DisplayType: "code/go",
					Optional:    false,
					Dynamic:     false,
					Value:       "",
				},
			},
			Inputs: map[string]string{
				"script": `package main
					import (

				"context"
				"fmt"
				)
					
					func Exec(ctx context.Context, params map[string]interface{}) (rsp map[string]interface{}, err error) {
						return map[string]interface{}{"msg": fmt.Sprintf("hello %v, your age is: %v", params["name"], params["age"])}, nil
					}
					`,
			},
			OutputAnchors: []model.NodeOutputAnchor{
				{
					Name: map[string]string{
						"zh-CN": "信息",
					},
					Key:  "msg",
					Type: "string",
					List: false,
				},
			},
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	//t.Logf("%+v", m)

	r, err := cmd.Exec(nil, map[string]interface{}{
		"name": "bysir",
		"age":  18,
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, out := range m.Outputs {
		t.Logf("%v: %v", out.Key, r[out.Key])
	}
}
