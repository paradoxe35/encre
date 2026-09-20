package platform

type AutoStart interface {
	Enable() error
	Disable() error
	IsEnabled() bool
}

func GetAutoStart() AutoStart {
	return &autoStart{}
}
