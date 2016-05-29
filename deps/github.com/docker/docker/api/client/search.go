package client

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"text/tabwriter"

	"golang.org/x/net/context"

	Cli "github.com/docker/docker/cli"
	"github.com/docker/docker/opts"
	flag "github.com/docker/docker/pkg/mflag"
	"github.com/docker/docker/pkg/stringutils"
	"github.com/docker/docker/registry"
	"github.com/docker/engine-api/types"
	"github.com/docker/engine-api/types/filters"
	registrytypes "github.com/docker/engine-api/types/registry"
)

// CmdSearch searches the Docker Hub for images.
//
// Usage: docker search [OPTIONS] TERM
func (cli *DockerCli) CmdSearch(args ...string) error {
	var (
		err error

		filterArgs = filters.NewArgs()

		flFilter = opts.NewListOpts(nil)
	)

	cmd := Cli.Subcmd("search", []string{"TERM"}, Cli.DockerCommands["search"].Description, true)
	noTrunc := cmd.Bool([]string{"-no-trunc"}, false, "Don't truncate output")
	cmd.Var(&flFilter, []string{"f", "-filter"}, "Filter output based on conditions provided")

	// Deprecated since Docker 1.12 in favor of "--filter"
	automated := cmd.Bool([]string{"#-automated"}, false, "Only show automated builds - DEPRECATED")
	stars := cmd.Uint([]string{"s", "#-stars"}, 0, "Only displays with at least x stars - DEPRECATED")

	cmd.Require(flag.Exact, 1)

	cmd.ParseFlags(args, true)

	for _, f := range flFilter.GetAll() {
		if filterArgs, err = filters.ParseFlag(f, filterArgs); err != nil {
			return err
		}
	}

	name := cmd.Arg(0)
	v := url.Values{}
	v.Set("term", name)

	indexInfo, err := registry.ParseSearchIndexInfo(name)
	if err != nil {
		return err
	}

	ctx := context.Background()

	authConfig := cli.resolveAuthConfig(ctx, indexInfo)
	requestPrivilege := cli.registryAuthenticationPrivilegedFunc(indexInfo, "search")

	encodedAuth, err := encodeAuthToBase64(authConfig)
	if err != nil {
		return err
	}

	options := types.ImageSearchOptions{
		RegistryAuth:  encodedAuth,
		PrivilegeFunc: requestPrivilege,
		Filters:       filterArgs,
	}

	unorderedResults, err := cli.client.ImageSearch(ctx, name, options)
	if err != nil {
		return err
	}

	results := searchResultsByStars(unorderedResults)
	sort.Sort(results)

	w := tabwriter.NewWriter(cli.out, 10, 1, 3, ' ', 0)
	fmt.Fprintf(w, "NAME\tDESCRIPTION\tSTARS\tOFFICIAL\tAUTOMATED\n")
	for _, res := range results {
		// --automated and -s, --stars are deprecated since Docker 1.12
		if (*automated && !res.IsAutomated) || (int(*stars) > res.StarCount) {
			continue
		}
		desc := strings.Replace(res.Description, "\n", " ", -1)
		desc = strings.Replace(desc, "\r", " ", -1)
		if !*noTrunc && len(desc) > 45 {
			desc = stringutils.Truncate(desc, 42) + "..."
		}
		fmt.Fprintf(w, "%s\t%s\t%d\t", res.Name, desc, res.StarCount)
		if res.IsOfficial {
			fmt.Fprint(w, "[OK]")

		}
		fmt.Fprint(w, "\t")
		if res.IsAutomated {
			fmt.Fprint(w, "[OK]")
		}
		fmt.Fprint(w, "\n")
	}
	w.Flush()
	return nil
}

// SearchResultsByStars sorts search results in descending order by number of stars.
type searchResultsByStars []registrytypes.SearchResult

func (r searchResultsByStars) Len() int           { return len(r) }
func (r searchResultsByStars) Swap(i, j int)      { r[i], r[j] = r[j], r[i] }
func (r searchResultsByStars) Less(i, j int) bool { return r[j].StarCount < r[i].StarCount }