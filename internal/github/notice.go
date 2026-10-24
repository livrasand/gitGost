package github

// separator + footer appended to anonymous PRs, MRs and issues.
var anonNotice = "\n\n---\n\n" +
	"*gitGost helps developers protect their privacy by removing identifying information from commits and squashing changes.*\n\n" +
	"*While this may limit context for maintainers, remember that not everyone enjoys the same freedoms. For some, privacy is a necessity.*\n\n" +
	"*Learn more at [gitGost](https://gitgost.livrasand.com).*"

// AnonNotice returns the markdown footer appended to anonymous contributions.
func AnonNotice() string {
	return anonNotice
}
