package drift

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	fwk "k8s.io/kube-scheduler/framework"
)

var GpuNumTypeList = []string{"PureCpu", "ShareGpu", "OneGpu", "TwoGpu", "FourGpu", "EightGpu", "Others"}

const (
	Q1LackBoth  = "q1_lack_both"
	Q2LackGpu   = "q2_lack_gpu"
	Q3Satisfied = "q3_satisfied"
	Q4LackCpu   = "q4_lack_cpu"
	XLSatisfied = "xl_satisfied"
	XRLackCPU   = "xr_lack_cpu"
	NoAccess    = "no_access"
)

var FragRatioDataMap = map[string]int{
	Q1LackBoth:  0,
	Q2LackGpu:   1,
	Q3Satisfied: 2,
	Q4LackCpu:   3,
	XLSatisfied: 4,
	XRLackCPU:   5,
	NoAccess:    6,
}

func GetTypicalPods(allPods []*v1.Pod) TargetPodList {
	tgtPodResCntMap := map[PodResource]float64{}
	podGpuCntMap := map[string]int64{}
	for _, v := range GpuNumTypeList {
		podGpuCntMap[v] = 0
	}
	var total float64 = 0
	for _, pod := range allPods {
		tgtPodRes := GetPodResource(pod)

		var weightedCnt float64 = 1
		if cnt, ok := tgtPodResCntMap[tgtPodRes]; ok {
			tgtPodResCntMap[tgtPodRes] = cnt + weightedCnt
		} else {
			tgtPodResCntMap[tgtPodRes] = weightedCnt
		}
		total += weightedCnt

		switch tgtPodRes.GpuNumber {
		case 0:
			podGpuCntMap[GpuNumTypeList[0]] += 1 // CPU
		case 1:
			if tgtPodRes.MilliGpu < GPUPointsPerCard {
				podGpuCntMap[GpuNumTypeList[1]] += 1 // ShareGpu
			} else {
				podGpuCntMap[GpuNumTypeList[2]] += 1 // OneGpu
			}
		case 2:
			podGpuCntMap[GpuNumTypeList[3]] += 1 // TwoGpu
		case 4:
			podGpuCntMap[GpuNumTypeList[4]] += 1 // FourGpu
		case 8:
			podGpuCntMap[GpuNumTypeList[5]] += 1 // EightGpu
		default:
			podGpuCntMap[GpuNumTypeList[6]] += 1 // Others
		}
	}

	tgtPodList := SortTargetPodInDecreasingCount(tgtPodResCntMap)
	// log.Infof("Num of Total Pods: %d\n", len(allPods))
	// for _, k := range GpuNumTypeList { // iter List, instead of Map, to guarantee order
	// 	log.Infof("  %s Pods: %d (%.2f%%)\n", k, podGpuCntMap[k], 100.0*float64(podGpuCntMap[k])/total)
	// }
	// log.Infof("Num of Total Pod Sepc: %d\n", len(tgtPodList))
	var expectedNumPods float64 = 0
	expectedNumPods = float64(DefaultTypicalPodPopularityThreshold) * total / 100.0
	var i, podResNum int
	var cumNumPods float64 = 0
	for cumNumPods < expectedNumPods {
		podResNum += DefaultTypicalPodIncreaseStep
		for i < podResNum && i < len(tgtPodList) {
			numPods := tgtPodList[i].Percentage
			cumNumPods += numPods
			tgtPodList[i].Percentage = tgtPodList[i].Percentage / total // normalized to 0.0-1.0 (100%)
			// ratioPct := 100 * tgtPodList[i].Percentage
			// cumRatioPct := 100 * cumNumPods / total
			// log.Infof("[%d] %s: %.0f (%.2f%%, cumsum: %.2f%%)\n", i, tgtPodList[i].TargetPodResource.Repr(), numPods, ratioPct, cumRatioPct)
			i += 1
		}
	}

	// log.Infof("Count top %d pod resource spec as typical ones, accounting for %.2f%% of all pods\n", i, 100.0*cumNumPods/total)
	// log.Infoln()

	if i >= len(tgtPodList) {
		return tgtPodList
	} else {
		outPodList := tgtPodList[:i] // chopping at i-th pods
		// normalize Percentage to 0.0 - 1.0 after chopping i-th pods
		var cumRatioPct float64
		for j := 0; j < i; j++ {
			outPodList[j].Percentage /= cumNumPods / total
			cumRatioPct += outPodList[j].Percentage
		}
		// if math.Abs(cumRatioPct-1) > 1e-3 {
		// 	log.Errorf("Renormalization fails (%.4f != 1.0): %v\n", cumRatioPct, outPodList)
		// }
		return outPodList
	}
}

func SortTargetPodInDecreasingCount(tgtPodResMap map[PodResource]float64) TargetPodList {
	pl := make(TargetPodList, len(tgtPodResMap))
	i := 0
	for k, v := range tgtPodResMap {
		pl[i] = TargetPod{TargetPodResource: k, Percentage: v}
		i++
	}
	sort.Sort(sort.Reverse(pl))
	return pl
}

func GetNodeResourceViaNodeInfo(nodeInfo fwk.NodeInfo) (nodeRes NodeResource) {
	node := nodeInfo.Node()
	milliCpuLeft := node.Status.Allocatable.Cpu().MilliValue() - nodeInfo.GetRequested().GetMilliCPU()

	milliGpuLeftList := []int64{}
	gpuNum := int(getGpuCountFromNode(nodeInfo))
	leftGpuPoints := gpuNum*int(GPUPointsPerCard) - int(gpuPointsUsedOnNode(nodeInfo))
	for i := 0; i < gpuNum; i++ {
		avgGpuPointsLeft := int64(leftGpuPoints / gpuNum)
		milliGpuLeftList = append(milliGpuLeftList, avgGpuPointsLeft)
	}

	return NodeResource{
		NodeName:         node.Name,
		MilliCpuLeft:     milliCpuLeft,
		MilliCpuCapacity: node.Status.Allocatable.Cpu().MilliValue(),
		MilliGpuLeftList: milliGpuLeftList,
		GpuNumber:        gpuNum,
		GpuType:          "",
		// GpuAffinity:      nodeGpuAffinity,
	}
}

func GetPodResource(pod *v1.Pod) PodResource {
	gpuNumber, _ := gpuCountFromPod(pod)
	gpuMilli, _ := gpuPointsFromPod(pod)
	gpuType := ""

	var non0CPU, non0Mem int64
	for _, c := range pod.Spec.Containers {

		non0CPUReq, non0MemReq := GetNonzeroRequests(&c.Resources.Requests)
		non0CPU += non0CPUReq
		non0Mem += non0MemReq
	}

	tgtPodRes := PodResource{
		MilliCpu:  non0CPU,
		MilliGpu:  gpuMilli,
		GpuNumber: int(gpuNumber),
		GpuType:   gpuType,
	}
	return tgtPodRes
}

func initTypicalPods() *TargetPodList {
	allPods := allPodsDemo()
	typicalPods := GetTypicalPods(allPods)
	return &typicalPods
}

// allPodsDemo 初始化包含GPU标签和CPU/内存资源的Pod列表
func allPodsDemo() []*v1.Pod {
	podNames := []string{"p1", "p2", "p3", "p4", "p5", "p6"}
	podGPUPoints := []int{200, 300, 400, 500, 800, 1000}
	podGPUCounts := []int{1, 1, 1, 1, 1, 1}
	podCpus := []int{4000, 4000, 4000, 4000, 6000, 8000}       // cpu资源需求，单位m
	podMems := []int{30517, 30517, 30517, 30517, 30517, 30517} // mem资源需求，单位Mi

	// 边界检查：确保所有参数切片长度一致，避免索引越界
	paramLengths := []int{
		len(podNames),
		len(podGPUPoints),
		len(podGPUCounts),
		len(podCpus),
		len(podMems),
	}
	baseLen := paramLengths[0]
	for _, l := range paramLengths[1:] {
		if l != baseLen {
			panic("allPodsDemo: 所有Pod参数切片长度必须一致")
		}
	}

	pods := []*v1.Pod{}
	for i := range podNames {
		// 1. 构建GPU相关标签（转为字符串，匹配解析函数的int64解析逻辑）
		labels := map[string]string{
			GPUReqCountLabelKey: strconv.Itoa(podGPUCounts[i]),
			GPUPointsLabelKey:   strconv.Itoa(podGPUPoints[i]),
		}

		// 2. 解析CPU/内存资源请求（符合K8s资源格式规范）
		cpuRequest := resource.MustParse(strconv.Itoa(podCpus[i]) + "m")
		memRequest := resource.MustParse(strconv.Itoa(podMems[i]) + "Mi")

		// 3. 构建Pod对象
		pod := &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:   podNames[i], // Pod名称
				Labels: labels,      // 包含GPU标签
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Name: "gpu-workload", // 容器名称可根据业务调整
						Resources: v1.ResourceRequirements{
							Requests: v1.ResourceList{
								v1.ResourceCPU:    cpuRequest,
								v1.ResourceMemory: memRequest,
							},
						},
					},
				},
			},
		}

		pods = append(pods, pod)
	}

	return pods
}

func NodeGpuShareFragAmountScore(nodeRes NodeResource, typicalPods TargetPodList) float64 {
	fragAmount := NodeGpuShareFragAmount(nodeRes, typicalPods)
	return fragAmount.FragAmountSumExceptQ3()
}

func (fa FragAmount) FragAmountSumExceptQ3() (out float64) {
	for i := 0; i < len(FragRatioDataMap); i++ {
		if i != FragRatioDataMap[Q3Satisfied] {
			out += fa.Data[i]
		}
	}
	return out
}

type FragAmount struct {
	NodeName string
	Data     []float64
}

func NewFragAmount(nodeName string, data []float64) FragAmount {
	fragAmount := FragAmount{NodeName: nodeName, Data: make([]float64, len(data))}
	copy(fragAmount.Data, data)
	return fragAmount
}

func NodeGpuShareFragAmount(nodeRes NodeResource, typicalPods TargetPodList) FragAmount {
	data := make([]float64, len(FragRatioDataMap))
	fragAmount := NewFragAmount(nodeRes.NodeName, data)
	for _, pod := range typicalPods {
		freq := pod.Percentage
		if freq < 0 || freq > 1 {
			continue
		}
		fragType := GetNodePodFrag(nodeRes, pod.TargetPodResource)
		gpuMilliLeftTotal := GetGpuMilliLeftTotal(nodeRes)
		if fragType == Q3Satisfied { // Part of GPUs are treated as Lack GPU fragment
			gpuFragMilli := GetGpuFragMilliByNodeResAndPodRes(nodeRes, pod.TargetPodResource)
			fragAmount.AddByFragType(Q2LackGpu, freq*float64(gpuFragMilli))
			fragAmount.AddByFragType(Q3Satisfied, freq*float64(gpuMilliLeftTotal-gpuFragMilli))
		} else { // Q1, Q2, XL, XR, NA => all idle GPU resources are treated as fragment
			fragAmount.AddByFragType(fragType, freq*float64(gpuMilliLeftTotal))
		}
	}
	return fragAmount
}

func (fa FragAmount) AddByFragType(fragType string, amount float64) error {
	if amount < 0 {
		return fmt.Errorf("bad freq")
	}
	if index, ok := FragRatioDataMap[fragType]; !ok {
		return fmt.Errorf("bad fragType")
	} else {
		fa.Data[index] += amount
		return nil
	}
}

func GetGpuFragMilliByNodeResAndPodRes(nodeRes NodeResource, podRes PodResource) int64 {
	gpuFragMilli := int64(0)
	for _, milliGpuLeft := range nodeRes.MilliGpuLeftList {
		if milliGpuLeft < podRes.MilliGpu {
			gpuFragMilli += milliGpuLeft
		}
	}
	return gpuFragMilli
}

func GetGpuMilliLeftTotal(nodeRes NodeResource) (gpuMilliLeftTotal int64) {
	for _, gpuMilliLeft := range nodeRes.MilliGpuLeftList {
		gpuMilliLeftTotal += gpuMilliLeft
	}
	return gpuMilliLeftTotal
}

func GetNodePodFrag(nodeRes NodeResource, podRes PodResource) string {
	if podRes.MilliGpu == 0 {
		if nodeRes.MilliCpuLeft >= podRes.MilliCpu {
			return XLSatisfied
		} else {
			return XRLackCPU
		}
	}

	if IsNodeAccessibleToPod(nodeRes, podRes) == false {
		return NoAccess
	}

	if CanNodeHostPodOnGpuMemory(nodeRes, podRes) {
		if nodeRes.MilliCpuLeft >= podRes.MilliCpu {
			return Q3Satisfied
		} else {
			return Q4LackCpu
		}
	} else {
		if nodeRes.MilliCpuLeft >= podRes.MilliCpu {
			return Q2LackGpu
		} else {
			return Q1LackBoth
		}
	}
}

func IsNodeAccessibleToPod(nodeRes NodeResource, podRes PodResource) bool {
	pt := podRes.GpuType
	nt := nodeRes.GpuType
	return IsNodeAccessibleToPodByType(nt, pt)
}

func IsNodeAccessibleToPodByType(nodeGpuType string, podGpuType string) bool {
	if len(podGpuType) == 0 {
		return true
	}
	if len(nodeGpuType) == 0 {
		return false // i.e., CPU node
	}

	podGpuTypeList := strings.Split(podGpuType, "|")
	cnt := 0
	for _, gpuType := range podGpuTypeList {
		if len(gpuType) == 0 {
			continue
		}
		cnt++
		if gpuType == nodeGpuType {
			return true
		}
	}
	if cnt > 0 { // pod requests at least one specific GPU type but node doesn't match
		return false
	} else { // pod actually doesn't request any specific GPU type
		return true
	}
}

func CanNodeHostPodOnGpuMemory(nodeRes NodeResource, podRes PodResource) bool {
	gpuRequest := podRes.GpuNumber
	for _, gpuHostMem := range nodeRes.MilliGpuLeftList {
		if gpuHostMem >= podRes.MilliGpu {
			gpuRequest -= 1
			if gpuRequest <= 0 {
				return true
			}
		}
	}
	return false
}
