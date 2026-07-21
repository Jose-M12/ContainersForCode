package manifest

func BuiltinDefaults() Defaults {
	stop := true
	return Defaults{
		APIVersion: APIVersion,
		Kind:       DefaultsKind,
		Spec: DefaultsSpec{
			SecurityClass:   "development",
			ResourceClass:   "balanced",
			StrictResources: true,
			AuditMaxMiB:     10,
			StopOnShellExit: &stop,
		},
	}
}

func ApplyProfileDefaults(profile *Profile) {
	if profile.Spec.Image.PullPolicy == "" {
		profile.Spec.Image.PullPolicy = "newer"
	}
	if profile.Spec.Defaults.SecurityClass == "" {
		profile.Spec.Defaults.SecurityClass = "development"
	}
	if profile.Spec.Defaults.ResourceClass == "" {
		profile.Spec.Defaults.ResourceClass = "balanced"
	}
	if profile.Spec.Defaults.RootFSPersistence == "" {
		profile.Spec.Defaults.RootFSPersistence = "persistent"
	}
	for i := range profile.Spec.Checks {
		if profile.Spec.Checks[i].TimeoutSeconds == 0 {
			profile.Spec.Checks[i].TimeoutSeconds = 30
		}
	}
}

func ApplyEnvironmentDefaults(environment *Environment, profile Profile, defaults Defaults) {
	if environment.Spec.SecurityClass == "" {
		environment.Spec.SecurityClass = profile.Spec.Defaults.SecurityClass
		if environment.Spec.SecurityClass == "" {
			environment.Spec.SecurityClass = defaults.Spec.SecurityClass
		}
	}
	if environment.Spec.ResourceClass == "" {
		environment.Spec.ResourceClass = profile.Spec.Defaults.ResourceClass
		if environment.Spec.ResourceClass == "" {
			environment.Spec.ResourceClass = defaults.Spec.ResourceClass
		}
	}
	if environment.Spec.Home.Persistence == "" {
		environment.Spec.Home.Persistence = "per-environment"
	}
	if environment.Spec.RootFS.Persistence == "" {
		environment.Spec.RootFS.Persistence = profile.Spec.Defaults.RootFSPersistence
	}
	if environment.Spec.Concurrency.ProjectMode == "" {
		environment.Spec.Concurrency.ProjectMode = "exclusive"
	}
	if environment.Spec.Network.Mode == "" {
		environment.Spec.Network.Mode = "outbound"
	}
	if environment.Spec.Project != nil {
		if environment.Spec.Project.Target == "" {
			environment.Spec.Project.Target = profile.Spec.Runtime.Workdir
		}
		if environment.Spec.Project.Mode == "" {
			environment.Spec.Project.Mode = "read-write"
		}
	}
	for i := range environment.Spec.Network.Ports {
		if environment.Spec.Network.Ports[i].HostIP == "" {
			environment.Spec.Network.Ports[i].HostIP = "127.0.0.1"
		}
		if environment.Spec.Network.Ports[i].Protocol == "" {
			environment.Spec.Network.Ports[i].Protocol = "tcp"
		}
	}
	for i := range environment.Spec.Secrets {
		if environment.Spec.Secrets[i].Mode == 0 {
			environment.Spec.Secrets[i].Mode = 0400
		}
	}
}

func ApplyDefaultsDefaults(defaults *Defaults) {
	builtin := BuiltinDefaults()
	if defaults.Spec.SecurityClass == "" {
		defaults.Spec.SecurityClass = builtin.Spec.SecurityClass
	}
	if defaults.Spec.ResourceClass == "" {
		defaults.Spec.ResourceClass = builtin.Spec.ResourceClass
	}
	if defaults.Spec.AuditMaxMiB == 0 {
		defaults.Spec.AuditMaxMiB = builtin.Spec.AuditMaxMiB
	}
	if defaults.Spec.StopOnShellExit == nil {
		defaults.Spec.StopOnShellExit = builtin.Spec.StopOnShellExit
	}
}
