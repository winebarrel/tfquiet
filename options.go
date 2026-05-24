package tfquiet

type options struct {
	showMoved   bool
	showImport  bool
	showRemoved bool
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

func OptionShowImport(v bool) OptFn {
	return func(o *options) {
		o.showImport = v
	}
}

func OptionShowRemoved(v bool) OptFn {
	return func(o *options) {
		o.showRemoved = v
	}
}

func OptionShowNoise(v bool) OptFn {
	return func(o *options) {
		o.showNoise = v
	}
}
