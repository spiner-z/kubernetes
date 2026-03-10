package drift

import (
	"context"
	"fmt"

	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/names"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	fwk "k8s.io/kube-scheduler/framework"
)

var _ fwk.FilterPlugin = &DriftPlugin{}
var _ fwk.ScorePlugin = &DriftPlugin{}

const Name = names.Drift

func New(_ context.Context, _ runtime.Object, handle fwk.Handle) (fwk.Plugin, error) {
	// return &DefaultBinder{handle: handle}, nil
	t := initTypicalPods()
	return &DriftPlugin{handle: handle, typicalPods: t}, nil
}

// 插件结构体定义
type DriftPlugin struct {
	handle      fwk.Handle // 调度器可以通过 handle 访问集群状态等，暂不使用
	typicalPods *TargetPodList
}

// Name 方法返回插件名称
func (dp *DriftPlugin) Name() string {
	return "DriftPlugin"
}

func (dp *DriftPlugin) Filter(ctx context.Context, state fwk.CycleState, pod *v1.Pod, nodeInfo fwk.NodeInfo) *fwk.Status {
	need, st := gpuPointsFromPod(pod)
	if st != nil && !st.IsSuccess() {
		return st
	}
	if need == 0 {
		// Pod 不需要 GPU，直接通过
		return fwk.NewStatus(fwk.Success, "")
	}
	if need > GPUPointsPerCard {
		return fwk.NewStatus(
			fwk.Unschedulable,
			fmt.Sprintf("pod gpu-points %d exceeds per-card limit %d", need, GPUPointsPerCard),
		)
	}
	nodeRes := GetNodeResourceViaNodeInfo(nodeInfo)
	podRes := GetPodResource(pod)
	if !IsNodeAccessibleToPod(nodeRes, podRes) {
		return fwk.NewStatus(fwk.Unschedulable, fmt.Sprintf("Node (%s) %s does not match GPU type request of pod %s\n", nodeInfo.Node().Name, nodeRes.Repr(), podRes.Repr()))
	}
	return fwk.NewStatus(fwk.Success, "")
}

// func (dp *DriftPlugin) Score(ctx context.Context, state *framework.CycleState,
// 	pod *v1.Pod, nodeName string) (int64, *framework.Status) {

// 	// 通过 handle 或框架获取 nodeInfo
// 	nodeInfo, err := dp.handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
// 	if err != nil {
// 		// 获取节点信息出错，返回错误状态
// 		return 0, framework.NewStatus(framework.Error, fmt.Sprintf("获取节点信息失败: %v", err))
// 	}

// 	allocable := nodeInfo.Allocatable // 可分配资源
// 	requested := nodeInfo.Requested   // 已请求资源

// 	// 避免除零错误
// 	if allocable.MilliCPU == 0 || allocable.Memory == 0 {
// 		return 0, framework.NewStatus(framework.Error, "节点资源数据异常")
// 	}

// 	// 计算CPU和内存剩余百分比（0~1之间）
// 	freeCPUFrac := float64(allocable.MilliCPU-requested.MilliCPU) / float64(allocable.MilliCPU)
// 	freeMemFrac := float64(allocable.Memory-requested.Memory) / float64(allocable.Memory)

// 	if freeCPUFrac < 0 {
// 		freeCPUFrac = 0
// 	}
// 	if freeMemFrac < 0 {
// 		freeMemFrac = 0
// 	}

// 	// 按权重计算综合得分（CPU权重0.8，内存权重0.2）
// 	rate := freeCPUFrac*0.8 + freeMemFrac*0.2

// 	needGPU, st := gpuPointsFromPod(pod)
// 	if st != nil {
// 		return 0, st
// 	}

// 	finalRate := rate
// 	if needGPU > 0 {
// 		// Pod 需要 GPU，考虑 GPU 资源
// 		capPoints, st := gpuCapacityPointsFromLabel(nodeInfo)
// 		if st != nil {
// 			return 0, st
// 		}
// 		usedPoints := gpuPointsUsedOnNode(nodeInfo)
// 		gpuRate := float64(capPoints-usedPoints) / float64(capPoints)
// 		if gpuRate < 0 {
// 			gpuRate = 0
// 		}
// 		// 综合 CPU、内存和 GPU 得分（GPU 权重0.4，CPU+内存权重0.6）
// 		finalRate = (rate*0.6 + gpuRate*0.4)
// 	}

// 	// 转换为调度器要求的整数分值 (0~100)
// 	score := int64(finalRate * 100)
// 	if score < 0 {
// 		score = 0
// 	}
// 	if score > 100 {
// 		score = 100
// 	}
// 	return score, framework.NewStatus(framework.Success, "")
// }

// Score invoked at the score extension point.
func (dp *DriftPlugin) Score(ctx context.Context, state fwk.CycleState, pod *v1.Pod, nodeInfo fwk.NodeInfo) (int64, *fwk.Status) {

	// func (dp *DriftPlugin) Score(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeName string) (int64, *fwk.Status) {
	var nodeRes NodeResource
	var podRes PodResource
	// 通过 handle 或框架获取 nodeInfo
	// nodeInfo, err := dp.handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
	// if err != nil {
	// 	// 获取节点信息出错，返回错误状态
	// 	return 0, fwk.NewStatus(fwk.Error, fmt.Sprintf("获取节点信息失败: %v", err))
	// }
	nodeRes = GetNodeResourceViaNodeInfo(nodeInfo)
	podRes = GetPodResource(pod)
	if !IsNodeAccessibleToPod(nodeRes, podRes) {
		return fwk.MinNodeScore, fwk.NewStatus(fwk.Error, fmt.Sprintf("Node (%s) %s does not match GPU type request of pod %s\n", nodeInfo.Node().Name, nodeRes.Repr(), podRes.Repr()))
	}
	score := calculateGpuShareFragExtendScore(nodeRes, podRes, dp.typicalPods)
	return score, fwk.NewStatus(fwk.Success)
}

// ScoreExtensions 可选接口，用于提供NormalizeScore等能力。这里不需要额外处理，返回 nil。
func (dp *DriftPlugin) ScoreExtensions() fwk.ScoreExtensions {
	return nil
}

func calculateGpuShareFragExtendScore(nodeRes NodeResource, podRes PodResource, typicalPods *TargetPodList) (score int64) {
	nodeGpuShareFragScore := NodeGpuShareFragAmountScore(nodeRes, *typicalPods)
	if podRes.GpuNumber == 1 && podRes.MilliGpu < GPUPointsPerCard { // request partial GPU
		score = 0
		for i := 0; i < len(nodeRes.MilliGpuLeftList); i++ {
			if nodeRes.MilliGpuLeftList[i] >= podRes.MilliGpu {
				newNodeRes := nodeRes.Copy()
				newNodeRes.MilliCpuLeft -= podRes.MilliCpu
				newNodeRes.MilliGpuLeftList[i] -= podRes.MilliGpu
				newNodeGpuShareFragScore := NodeGpuShareFragAmountScore(newNodeRes, *typicalPods)
				fragScore := int64(sigmoid((nodeGpuShareFragScore-newNodeGpuShareFragScore)/1000) * float64(fwk.MaxNodeScore))
				if fragScore > score {
					score = fragScore
				}
			}
		}
		return score
	} else {
		newNodeRes, _ := nodeRes.Sub(podRes)
		newNodeGpuShareFragScore := NodeGpuShareFragAmountScore(newNodeRes, *typicalPods)
		return int64(sigmoid((nodeGpuShareFragScore-newNodeGpuShareFragScore)/1000) * float64(fwk.MaxNodeScore))
	}
}
