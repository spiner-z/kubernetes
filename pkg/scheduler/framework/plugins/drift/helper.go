package drift

import (
	"math"
	"strconv"

	v1 "k8s.io/api/core/v1"
	fwk "k8s.io/kube-scheduler/framework"
)

const (
	GPUPointsLabelKey         = "drift.io/gpu-points"  // for both
	GPUReqCountLabelKey       = "drift.io/gpu-count"   // for pod
	GPUCountLabelKey          = "nvidia.com/gpu.count" // for node
	GPUPointsPerCard    int64 = 1000
)

func gpuPointsUsedOnNode(ni fwk.NodeInfo) int64 {
	if ni == nil {
		return 0
	}
	var sum int64
	for _, pi := range ni.GetPods() {
		if pi == nil || pi.GetPod() == nil {
			continue
		}
		// 跳过已结束 Pod（一般 NodeInfo 里不会留着，但加了更稳）
		if pi.GetPod().Status.Phase == v1.PodSucceeded || pi.GetPod().Status.Phase == v1.PodFailed {
			continue
		}
		points := pi.GetPod().Labels[GPUPointsLabelKey]
		if points == "" {
			continue
		}
		p, err := strconv.ParseInt(points, 10, 64)
		if err != nil || p <= 0 {
			continue
		}
		count := pi.GetPod().Labels[GPUReqCountLabelKey]
		var n int64 = 1
		if count != "" {
			n, err = strconv.ParseInt(count, 10, 64)
			if err != nil || n <= 0 {
				n = 1
			}
		}
		sum += p * n
	}
	return sum
}

func getGpuCountFromNode(ni fwk.NodeInfo) int64 {
	if ni == nil {
		return 0
	}
	node := ni.Node()
	if node == nil {
		return 0
	}
	s := node.Labels[GPUCountLabelKey]
	if s == "" {
		return 0
	}
	gpuCount, err := strconv.ParseInt(s, 10, 64)
	if err != nil || gpuCount < 0 {
		return 0
	}
	return gpuCount
}

func gpuCapacityPointsFromLabel(ni fwk.NodeInfo) (int64, *fwk.Status) {
	if ni == nil {
		return 0, fwk.NewStatus(fwk.Error, "nil nodeInfo")
	}
	node := ni.Node()
	if node == nil {
		return 0, fwk.NewStatus(fwk.Error, "node is nil in nodeInfo")
	}

	s := node.Labels[GPUCountLabelKey]
	if s == "" {
		// 没打这个标签，就认为 gpu_count=0
		return 0, nil
	}

	gpuCount, err := strconv.ParseInt(s, 10, 64)
	if err != nil || gpuCount < 0 {
		return 0, fwk.NewStatus(
			fwk.UnschedulableAndUnresolvable,
			"invalid node label nvidia.com/gpu.count (must be non-negative int)",
		)
	}
	return gpuCount * GPUPointsPerCard, nil
}

func gpuCountFromPod(pod *v1.Pod) (int64, *fwk.Status) {
	if pod == nil {
		return 0, fwk.NewStatus(fwk.Error, "nil pod")
	}
	val, ok := pod.Labels[GPUReqCountLabelKey]
	if !ok || val == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return 0, fwk.NewStatus(fwk.UnschedulableAndUnresolvable, "invalid gpu-count label (must be int64)")
	}
	return n, nil
}

func gpuPointsFromPod(pod *v1.Pod) (int64, *fwk.Status) {
	if pod == nil {
		return 0, fwk.NewStatus(fwk.Error, "nil pod")
	}
	val, ok := pod.Labels[GPUPointsLabelKey]
	if !ok || val == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return 0, fwk.NewStatus(fwk.UnschedulableAndUnresolvable, "invalid gpu-points label (must be int64)")
	}
	if n < 0 || n > GPUPointsPerCard {
		return 0, fwk.NewStatus(fwk.UnschedulableAndUnresolvable, "gpu-points must be in [0,1000]")
	}
	return n, nil
}

func sigmoid(x float64) float64 { //Sigmoid Activation
	return 1.0 / (1.0 + math.Exp(-x))
}
