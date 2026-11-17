package worker

import "fyredocs/shared/jobslots"

// classFor maps a tool to its workload class (shared/jobslots). Every
// convert-from-pdf tool runs an external engine (Poppler, Ghostscript, Python,
// LibreOffice), so all are cpu: at most CPU_JOB_SLOTS run at once.
func classFor(string) jobslots.Class { return jobslots.CPU }
