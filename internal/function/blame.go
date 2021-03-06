package function

import (
	"fmt"
	"io"

	"github.com/sirupsen/logrus"

	"github.com/src-d/gitbase"
	"github.com/src-d/go-mysql-server/sql"
	"gopkg.in/src-d/go-git.v4"

	"gopkg.in/src-d/go-git.v4/plumbing"
	"gopkg.in/src-d/go-git.v4/plumbing/object"
)

type BlameGenerator struct {
	ctx     *sql.Context
	commit  *object.Commit
	fIter   *object.FileIter
	curLine int
	curFile *object.File
	lines   []*git.Line
}

func NewBlameGenerator(ctx *sql.Context, c *object.Commit, f *object.FileIter) (*BlameGenerator, error) {
	return &BlameGenerator{ctx: ctx, commit: c, fIter: f, curLine: -1}, nil
}

func (g *BlameGenerator) loadNewFile() error {
	var err error
	g.curFile, err = g.fIter.Next()
	if err != nil {
		return err
	}

	result, err := git.Blame(g.commit, g.curFile.Name)
	if err != nil {
		msg := fmt.Sprintf(
			"Error in BLAME for file %s: %s",
			g.curFile.Name,
			err.Error(),
		)
		logrus.Warn(msg)
		g.ctx.Warn(0, msg)
		return io.EOF
	}

	if len(result.Lines) == 0 {
		return g.loadNewFile()
	}

	g.lines = result.Lines
	g.curLine = 0
	return nil
}

func (g *BlameGenerator) Next() (interface{}, error) {
	if g.curLine == -1 || g.curLine >= len(g.lines) {
		err := g.loadNewFile()
		if err != nil {
			return nil, err
		}
	}

	l := g.lines[g.curLine]
	b := BlameLine{
		File:    g.curFile.Name,
		LineNum: g.curLine,
		Author:  l.Author,
		Text:    l.Text,
	}
	g.curLine++
	return b, nil
}

func (g *BlameGenerator) Close() error {
	g.fIter.Close()
	return nil
}

var _ sql.Generator = (*BlameGenerator)(nil)

type (
	// Blame implements git-blame function as UDF
	Blame struct {
		repo   sql.Expression
		commit sql.Expression
	}

	// BlameLine represents each line of git blame's output
	BlameLine struct {
		File    string `json:"file"`
		LineNum int    `json:"linenum"`
		Author  string `json:"author"`
		Text    string `json:"text"`
	}
)

// NewBlame constructor
func NewBlame(repo, commit sql.Expression) sql.Expression {
	return &Blame{repo, commit}
}

func (b *Blame) String() string {
	return fmt.Sprintf("blame(%s, %s)", b.repo, b.commit)
}

// Type implements the sql.Expression interface
func (*Blame) Type() sql.Type {
	return sql.Array(sql.JSON)
}

func (b *Blame) WithChildren(children ...sql.Expression) (sql.Expression, error) {
	if len(children) != 2 {
		return nil, sql.ErrInvalidChildrenNumber.New(b, len(children), 2)
	}

	return NewBlame(children[0], children[1]), nil
}

// Children implements the Expression interface.
func (b *Blame) Children() []sql.Expression {
	return []sql.Expression{b.repo, b.commit}
}

// IsNullable implements the Expression interface.
func (b *Blame) IsNullable() bool {
	return b.repo.IsNullable() || (b.commit.IsNullable())
}

// Resolved implements the Expression interface.
func (b *Blame) Resolved() bool {
	return b.repo.Resolved() && b.commit.Resolved()
}

// Eval implements the sql.Expression interface.
func (b *Blame) Eval(ctx *sql.Context, row sql.Row) (interface{}, error) {
	span, ctx := ctx.Span("gitbase.Blame")
	defer span.Finish()

	repo, err := b.resolveRepo(ctx, row)
	if err != nil {
		ctx.Warn(0, err.Error())
		return nil, nil
	}

	commit, err := b.resolveCommit(ctx, repo, row)
	if err != nil {
		ctx.Warn(0, err.Error())
		return nil, nil
	}

	fIter, err := commit.Files()
	if err != nil {
		return nil, err
	}

	bg, err := NewBlameGenerator(ctx, commit, fIter)
	if err != nil {
		return nil, err
	}

	return bg, nil
}

func (b *Blame) resolveCommit(ctx *sql.Context, repo *gitbase.Repository, row sql.Row) (*object.Commit, error) {
	str, err := exprToString(ctx, b.commit, row)
	if err != nil {
		return nil, err
	}

	commitHash, err := repo.ResolveRevision(plumbing.Revision(str))
	if err != nil {
		h := plumbing.NewHash(str)
		commitHash = &h
	}
	to, err := repo.CommitObject(*commitHash)
	if err != nil {
		return nil, err
	}

	return to, nil
}

func (b *Blame) resolveRepo(ctx *sql.Context, r sql.Row) (*gitbase.Repository, error) {
	repoID, err := exprToString(ctx, b.repo, r)
	if err != nil {
		return nil, err
	}
	s, ok := ctx.Session.(*gitbase.Session)
	if !ok {
		return nil, gitbase.ErrInvalidGitbaseSession.New(ctx.Session)
	}
	return s.Pool.GetRepo(repoID)
}
