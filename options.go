package tfquiet

type options struct {
	showMoved   bool
	showDestroy bool
	showImport  bool
	showNoise   bool
}

func newOptions(optfns []OptFn) *options {
	opts := &options{}

	for _, f := range optfns {
		f(opts)
	}

	return opts
}

type OptFn func(*options)

func OptionShowMoved(v bool) OptFn {
	return func(o *options) {
		o.showMoved = v
	}
}

func OptionShowDestroy(v bool) OptFn {
	return func(o *options) {
		o.showDestroy = v
	}
}

func OptionShowImport(v bool) OptFn {
	return func(o *options) {
		o.showImport = v
	}
}

func OptionShowNoise(v bool) OptFn {
	return func(o *options) {
		o.showNoise = v
	}
}
