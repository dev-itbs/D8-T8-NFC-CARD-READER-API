package reader

import (
	"fmt"
	"syscall"
)

// DLLLoader manages the dc_sdk.dll loading and function access
type DLLLoader struct {
	dll   *syscall.DLL
	procs map[string]*syscall.Proc
}

// NewDLLLoader loads the DC SDK DLL and initializes all function pointers
func NewDLLLoader(dllName string) (*DLLLoader, error) {
	dll, err := syscall.LoadDLL(dllName)
	if err != nil {
		return nil, fmt.Errorf("failed to load %s: %w", dllName, err)
	}

	loader := &DLLLoader{
		dll:   dll,
		procs: make(map[string]*syscall.Proc),
	}

	// Load all required function pointers
	procNames := []string{
		"dc_init",
		"dc_exit",
		"dc_card",
		"dc_request",
		"dc_anticoll",
		"dc_select",
		"dc_load_key",
		"dc_authentication",
		"dc_read",
		"dc_write",
		"dc_halt",
		"dc_beep",
		"dc_getver",
		"dc_reset",
		"dc_srd_eeprom",
		"dc_swr_eeprom",
		"dc_initval",
		"dc_increment",
		"dc_decrement",
		"dc_readval",
		"dc_authentication_pass",
	}

	for _, name := range procNames {
		proc, err := dll.FindProc(name)
		if err != nil {
			return nil, fmt.Errorf("failed to find function %s: %w", name, err)
		}
		loader.procs[name] = proc
	}

	return loader, nil
}

// GetProc returns the syscall.Proc for a given function name
func (dl *DLLLoader) GetProc(name string) (*syscall.Proc, error) {
	proc, ok := dl.procs[name]
	if !ok {
		return nil, fmt.Errorf("function %s not loaded", name)
	}
	return proc, nil
}

// Close releases the DLL handle
func (dl *DLLLoader) Close() error {
	if dl.dll != nil {
		return dl.dll.Release()
	}
	return nil
}

// Call is a helper to call a DLL function and convert the return value to an int
func (dl *DLLLoader) Call(funcName string, args ...uintptr) (int, error) {
	proc, err := dl.GetProc(funcName)
	if err != nil {
		return 0, err
	}

	ret, _, err := proc.Call(args...)
	return int(ret), nil
}
