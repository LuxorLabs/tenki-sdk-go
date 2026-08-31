package sandbox

import "fmt"

const (
	minCPUCores          = int32(1)
	maxCPUCores          = int32(16)
	minCreateMemoryMB    = int32(128)
	minProvisionMemoryMB = int32(512)
	maxMemoryMB          = int32(65536)
	minDiskSizeGB        = int32(5)
	maxDiskSizeGB        = int32(100)
)

// ValidateCreateResources checks create resources against the sandbox API's
// constraints without issuing a request. Create applies it to every request;
// it is exported so callers building a form can reject one field at a time,
// passing nil for the fields they are not checking.
func ValidateCreateResources(cpuCores *int32, memoryMB *int32, diskSizeGB *int32) error {
	if cpuCores != nil {
		if err := validateCPUCores(*cpuCores); err != nil {
			return err
		}
	}
	if memoryMB != nil {
		if err := validateMemoryMB(*memoryMB, minCreateMemoryMB); err != nil {
			return err
		}
	}
	if diskSizeGB != nil {
		if err := validateDiskSizeGB(*diskSizeGB); err != nil {
			return err
		}
	}
	return nil
}

func validateTemplateResources(resources *TemplateResources) error {
	if resources == nil {
		return nil
	}
	if resources.CPUCores != 0 {
		if err := validateCPUCores(resources.CPUCores); err != nil {
			return err
		}
	}
	if resources.MemoryMB != 0 {
		if err := validateMemoryMB(resources.MemoryMB, minProvisionMemoryMB); err != nil {
			return err
		}
	}
	if resources.DiskSizeGB != 0 {
		if err := validateDiskSizeGB(resources.DiskSizeGB); err != nil {
			return err
		}
	}
	return nil
}

func validateCPUCores(cpuCores int32) error {
	if cpuCores >= minCPUCores && cpuCores <= maxCPUCores {
		return nil
	}
	return fmt.Errorf("%w: cpu_cores must be between %d and %d", ErrInvalidResourceConfig, minCPUCores, maxCPUCores)
}

func validateMemoryMB(memoryMB int32, minMemoryMB int32) error {
	if memoryMB < minMemoryMB || memoryMB > maxMemoryMB {
		return fmt.Errorf("%w: memory_mb must be between %d and %d", ErrInvalidResourceConfig, minMemoryMB, maxMemoryMB)
	}
	if memoryMB%2 != 0 {
		return fmt.Errorf("%w: memory_mb must be aligned to 2 MiB", ErrInvalidResourceConfig)
	}
	return nil
}

func validateDiskSizeGB(diskSizeGB int32) error {
	if diskSizeGB >= minDiskSizeGB && diskSizeGB <= maxDiskSizeGB {
		return nil
	}
	return fmt.Errorf("%w: disk_size_gb must be between %d and %d", ErrInvalidResourceConfig, minDiskSizeGB, maxDiskSizeGB)
}
