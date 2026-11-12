package api

import "errors"

func encodeComputerOperationRequest(request ComputerCommandRequest) (ComputerOperationRequest, error) {
	wire := ComputerOperationRequest{ComputerSessionId: request.ComputerSessionId, RequestId: request.RequestId}
	command, err := request.Command.ValueByDiscriminator()
	if err != nil {
		return wire, errors.New("computer command kind is invalid")
	}
	switch command := command.(type) {
	case ComputerExec:
		argv := make([]string, len(command.Argv))
		for index, argument := range command.Argv {
			argv[index] = EncodeComputerBytes([]byte(argument))
		}
		exec := ComputerExecRequest{ArgvB64: argv, Stdin: command.Stdin, TimeoutMs: command.TimeoutMs}
		if command.Cwd != nil {
			cwd := EncodeComputerBytes([]byte(*command.Cwd))
			exec.CwdB64 = &cwd
		}
		if command.Env != nil {
			env := make(map[string]string, len(*command.Env))
			for name, value := range *command.Env {
				env[name] = EncodeComputerBytes([]byte(value))
			}
			exec.EnvB64 = &env
		}
		err = wire.Command.FromComputerExecRequest(exec)
	case ComputerFileRead:
		err = wire.Command.FromComputerFileReadRequest(ComputerFileReadRequest{PathB64: EncodeComputerBytes([]byte(command.Path)), MaxBytes: command.MaxBytes, TimeoutMs: command.TimeoutMs})
	case ComputerFileWrite:
		err = wire.Command.FromComputerFileWriteRequest(ComputerFileWriteRequest{PathB64: EncodeComputerBytes([]byte(command.Path)), Data: command.Data, TimeoutMs: command.TimeoutMs})
	case ComputerFileStat:
		err = wire.Command.FromComputerFileStatRequest(ComputerFileStatRequest{PathB64: EncodeComputerBytes([]byte(command.Path)), TimeoutMs: command.TimeoutMs})
	case ComputerProcessList:
		err = wire.Command.FromComputerProcessList(command)
	case ComputerProcessInspect:
		err = wire.Command.FromComputerProcessInspect(command)
	case ComputerProcessTerminate:
		err = wire.Command.FromComputerProcessTerminate(command)
	default:
		err = errors.New("computer command kind is invalid")
	}
	return wire, err
}
