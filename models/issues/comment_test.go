// Copyright 2017 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package issues_test

import (
	"slices"
	"testing"
	"time"

	"gitea.dev/models/db"
	issues_model "gitea.dev/models/issues"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/optional"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateComment(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())

	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: issue.RepoID})
	doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})

	now := time.Now().Unix()
	comment, err := issues_model.CreateComment(t.Context(), &issues_model.CreateCommentOptions{
		Type:    issues_model.CommentTypeComment,
		Doer:    doer,
		Repo:    repo,
		Issue:   issue,
		Content: "Hello",
	})
	assert.NoError(t, err)
	then := time.Now().Unix()

	assert.Equal(t, issues_model.CommentTypeComment, comment.Type)
	assert.Equal(t, "Hello", comment.Content)
	assert.Equal(t, issue.ID, comment.IssueID)
	assert.Equal(t, doer.ID, comment.PosterID)
	unittest.AssertInt64InRange(t, now, then, int64(comment.CreatedUnix))
	unittest.AssertExistsAndLoadBean(t, comment) // assert actually added to DB

	updatedIssue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: issue.ID})
	unittest.AssertInt64InRange(t, now, then, int64(updatedIssue.UpdatedUnix))
}

func TestLoadAssigneeUserAndTeam_DeletedTeamBecomesGhostTeam(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())
	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 15})
	comment := &issues_model.Comment{
		Type:           issues_model.CommentTypeAssignees,
		IssueID:        issue.ID,
		AssigneeTeamID: 999999, // non-existing team ID
	}
	assert.NoError(t, comment.LoadAssigneeUserAndTeam(t.Context()))
	assert.NotNil(t, comment.AssigneeTeam)
	assert.EqualValues(t, -1, comment.AssigneeTeam.ID)
}

func Test_UpdateCommentAttachment(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())

	comment := unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{ID: 1})
	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: comment.IssueID})
	attachment := repo_model.Attachment{
		RepoID: issue.RepoID, // must match the comment's repo, else the cross-repo guard rejects it
		Name:   "test.txt",
	}
	assert.NoError(t, db.Insert(t.Context(), &attachment))

	err := issues_model.UpdateCommentAttachments(t.Context(), comment, []string{attachment.UUID})
	assert.NoError(t, err)

	attachment2 := unittest.AssertExistsAndLoadBean(t, &repo_model.Attachment{ID: attachment.ID})
	assert.Equal(t, attachment.Name, attachment2.Name)
	assert.Equal(t, comment.ID, attachment2.CommentID)
	assert.Equal(t, comment.IssueID, attachment2.IssueID)
}

func TestFetchCodeComments(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 2})
	reviewer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	reader := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

	comments, err := issues_model.FetchCodeComments(t.Context(), issue, reviewer, false)
	require.NoError(t, err)
	assert.Contains(t, comments["README.md"], int64(4))
	assert.Contains(t, comments["README.md"], int64(-4))

	comments, err = issues_model.FetchCodeComments(t.Context(), issue, reader, false)
	require.NoError(t, err)
	assert.NotContains(t, comments["README.md"], int64(4))
	assert.Contains(t, comments["README.md"], int64(-4))

	_, err = db.GetEngine(t.Context()).ID(4).Cols("review_id").Update(&issues_model.Comment{ReviewID: 999})
	require.NoError(t, err)
	comments, err = issues_model.FetchCodeComments(t.Context(), issue, reviewer, false)
	require.NoError(t, err)
	assert.NotContains(t, comments["README.md"], int64(4))
	assert.Contains(t, comments["README.md"], int64(-4))
}

func TestCommentVisibility(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	assertVisible := func(name string, user *user_model.User, expected bool) {
		comments, err := issues_model.FindComments(t.Context(), &issues_model.FindCommentsOptions{
			IssueID: 2, Type: issues_model.CommentTypeCode, VisibleToUser: optional.Some(user),
		})
		require.NoError(t, err)
		assert.True(t, slices.ContainsFunc(comments, func(comment *issues_model.Comment) bool { return comment.ID == 5 }), name)
		inCollection := slices.ContainsFunc(comments, func(comment *issues_model.Comment) bool { return comment.ID == 4 })
		_, err = issues_model.GetCommentWithRepoID(t.Context(), 1, 4, user)
		assert.Equal(t, expected, inCollection, name)
		assert.Equal(t, expected, err == nil, name)
		if !expected {
			assert.True(t, issues_model.IsErrCommentNotExist(err), name)
		}
	}

	assertVisible("anonymous", nil, false)
	assertVisible("reader", &user_model.User{ID: 2}, false)
	assertVisible("reviewer", &user_model.User{ID: 1}, true)
	assertVisible("administrator", &user_model.User{ID: 2, IsAdmin: true}, true)
	_, err := issues_model.GetCommentWithRepoID(t.Context(), 1, 5, nil)
	require.NoError(t, err)

	_, err = db.GetEngine(t.Context()).ID(4).Cols("type").Update(&issues_model.Review{Type: issues_model.ReviewTypeComment})
	require.NoError(t, err)
	assertVisible("submitted", &user_model.User{ID: 2}, true)

	_, err = db.GetEngine(t.Context()).ID(4).Cols("review_id").Update(&issues_model.Comment{ReviewID: 999})
	require.NoError(t, err)
	assertVisible("orphaned", &user_model.User{IsAdmin: true}, false)
}

func TestAsCommentType(t *testing.T) {
	assert.Equal(t, issues_model.CommentTypeComment, issues_model.CommentType(0))
	assert.Equal(t, issues_model.CommentTypeUndefined, issues_model.AsCommentType(""))
	assert.Equal(t, issues_model.CommentTypeUndefined, issues_model.AsCommentType("nonsense"))
	assert.Equal(t, issues_model.CommentTypeComment, issues_model.AsCommentType("comment"))
	assert.Equal(t, issues_model.CommentTypePRUnScheduledToAutoMerge, issues_model.AsCommentType("pull_cancel_scheduled_merge"))
}

func TestMigrate_InsertIssueComments(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())
	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 1})
	_ = issue.LoadRepo(t.Context())
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: issue.Repo.OwnerID})
	reaction := &issues_model.Reaction{
		Type:   "heart",
		UserID: owner.ID,
	}

	comment := &issues_model.Comment{
		PosterID:  owner.ID,
		Poster:    owner,
		IssueID:   issue.ID,
		Issue:     issue,
		Reactions: []*issues_model.Reaction{reaction},
	}

	err := issues_model.InsertIssueComments(t.Context(), []*issues_model.Comment{comment})
	assert.NoError(t, err)

	issueModified := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 1})
	assert.Equal(t, issue.NumComments+1, issueModified.NumComments)

	unittest.CheckConsistencyFor(t, &issues_model.Issue{})
}

func Test_UpdateIssueNumComments(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())
	issue2 := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 2})

	assert.NoError(t, issues_model.UpdateIssueNumComments(t.Context(), issue2.ID))
	issue2 = unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 2})
	assert.Equal(t, 1, issue2.NumComments)
}
