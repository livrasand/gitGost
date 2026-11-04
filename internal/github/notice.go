package github

var anonNotice = "\n\n---\n\n" +
	"*gitGost helps developers protect their privacy by removing identifying information from commits and squashing changes.*\n\n" +
	"*While this may limit context for maintainers, remember that not everyone enjoys the same freedoms. For some, privacy is a necessity.*\n\n" +
	"*Learn more at [gitGost](https://gitgost.livrasand.com).*"

func AnonNotice() string {
	return anonNotice
}
