package agent

func probeSMBWrite(path string) error {
	return ProbeWriteDir(path)
}
