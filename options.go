package tfquiet

type Options struct {
	ShowMoved   bool `kong:"help='Show moved blocks.'"`
	ShowImport  bool `kong:"help='Show import blocks.'"`
	ShowRemoved bool `kong:"help='Show removed{} lifecycle.destroy=false (state-only forget) blocks.'"`
	ShowDrift   bool `kong:"help='Show the \"Objects have changed outside of Terraform\" drift section.'"`
	ShowNoise   bool `kong:"help='Show refresh/lock lines and the trailing Note footer.'"`
}
