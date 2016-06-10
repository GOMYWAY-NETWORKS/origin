package integration

import (
	"fmt"
	"strings"
	"testing"
	"time"

	kapi "k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/api/resource"
	kclient "k8s.io/kubernetes/pkg/client/unversioned"

	"github.com/openshift/origin/pkg/client"
	imagetest "github.com/openshift/origin/pkg/image/admission/testutil"
	imageapi "github.com/openshift/origin/pkg/image/api"
	quotautil "github.com/openshift/origin/pkg/quota/util"
	testutil "github.com/openshift/origin/test/util"
	testserver "github.com/openshift/origin/test/util/server"
)

const limitRangeName = "limits"

func TestImageStreamTagsAdmission(t *testing.T) {
	kClient, client := setupImageStreamAdmissionTest(t)

	for i, name := range []string{imagetest.BaseImageWith1LayerDigest, imagetest.BaseImageWith2LayersDigest, imagetest.MiscImageDigest} {
		imageReference := fmt.Sprintf("openshift/test@%s", name)
		image := &imageapi.Image{
			ObjectMeta: kapi.ObjectMeta{
				Name: name,
			},
			DockerImageReference: imageReference,
		}
		tag := fmt.Sprintf("tag%d", i+1)

		err := client.ImageStreamMappings(testutil.Namespace()).Create(&imageapi.ImageStreamMapping{
			ObjectMeta: kapi.ObjectMeta{
				Name: "src",
			},
			Tag:   tag,
			Image: *image,
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	limit := kapi.ResourceList{imageapi.ResourceImageStreamTags: resource.MustParse("0")}
	lrClient := kClient.LimitRanges(testutil.Namespace())
	createLimitRangeOfType(t, lrClient, limitRangeName, imageapi.LimitTypeImageStream, limit)

	t.Logf("trying to create ImageStreamTag referencing isimage exceeding quota %v", limit)
	ist := &imageapi.ImageStreamTag{
		ObjectMeta: kapi.ObjectMeta{
			Name: "dest:tag1",
		},
		Tag: &imageapi.TagReference{
			Name: "1",
			From: &kapi.ObjectReference{
				Kind: "ImageStreamImage",
				Name: "src@" + imagetest.BaseImageWith1LayerDigest,
			},
		},
	}
	_, err := client.ImageStreamTags(testutil.Namespace()).Update(ist)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !quotautil.IsErrorQuotaExceeded(err) {
		t.Errorf("expected quota exceeded error, got instead: %v", err)
	}

	limit = bumpLimit(t, lrClient, limitRangeName, imageapi.ResourceImageStreamTags, "1")

	t.Logf("trying to create ImageStreamTag referencing isimage below quota %v", limit)
	ist = &imageapi.ImageStreamTag{
		ObjectMeta: kapi.ObjectMeta{
			Name: "dest:tag1",
		},
		Tag: &imageapi.TagReference{
			Name: "1",
			From: &kapi.ObjectReference{
				Kind: "ImageStreamImage",
				Name: "src@" + imagetest.BaseImageWith1LayerDigest,
			},
		},
	}
	// we may hit cache with old limit, let's retry in such a case
	err = retryOnQuotaExceeded(t, 1, func() error {
		ist, err = client.ImageStreamTags(testutil.Namespace()).Update(ist)
		return err
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	t.Logf("trying to create ImageStreamTag exceeding quota %v", limit)
	ist = &imageapi.ImageStreamTag{
		ObjectMeta: kapi.ObjectMeta{
			Name: "dest:tag2",
		},
		Tag: &imageapi.TagReference{
			Name: "2",
			From: &kapi.ObjectReference{
				Kind: "ImageStreamImage",
				Name: "src@" + imagetest.BaseImageWith2LayersDigest,
			},
		},
	}
	ist, err = client.ImageStreamTags(testutil.Namespace()).Update(ist)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !quotautil.IsErrorQuotaExceeded(err) {
		t.Errorf("expected quota exceeded error, got instead: %v", err)
	}

	t.Log("trying to create ImageStreamTag referencing isimage already referenced")
	ist = &imageapi.ImageStreamTag{
		ObjectMeta: kapi.ObjectMeta{
			Name: "dest:tag1again",
		},
		Tag: &imageapi.TagReference{
			Name: "tag1again",
			From: &kapi.ObjectReference{
				Kind: "ImageStreamImage",
				Name: "src@" + imagetest.BaseImageWith1LayerDigest,
			},
		},
	}
	_, err = client.ImageStreamTags(testutil.Namespace()).Update(ist)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	t.Log("trying to create ImageStreamTag in a new image stream")
	ist = &imageapi.ImageStreamTag{
		ObjectMeta: kapi.ObjectMeta{
			Name: "new:misc",
		},
		Tag: &imageapi.TagReference{
			Name: "misc",
			From: &kapi.ObjectReference{
				Kind: "ImageStreamImage",
				Name: "src@" + imagetest.MiscImageDigest,
			},
		},
	}
	_, err = client.ImageStreamTags(testutil.Namespace()).Update(ist)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	limit = bumpLimit(t, lrClient, limitRangeName, imageapi.ResourceImageStreamTags, "2")

	t.Logf("trying to create ImageStreamTag referencing istag below quota %v", limit)
	ist = &imageapi.ImageStreamTag{
		ObjectMeta: kapi.ObjectMeta{
			Name: "dest:tag2",
		},
		Tag: &imageapi.TagReference{
			Name: "2",
			From: &kapi.ObjectReference{
				Kind: "ImageStreamTag",
				Name: "src:tag2",
			},
		},
	}
	// we may hit cache with old limit, let's retry in such a case
	err = retryOnQuotaExceeded(t, 1, func() error {
		ist, err = client.ImageStreamTags(testutil.Namespace()).Update(ist)
		return err
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	t.Logf("trying to create ImageStreamTag referencing istag exceeding quota %v", limit)
	ist = &imageapi.ImageStreamTag{
		ObjectMeta: kapi.ObjectMeta{
			Name: "dest:tag3",
		},
		Tag: &imageapi.TagReference{
			Name: "3",
			From: &kapi.ObjectReference{
				Kind: "ImageStreamTag",
				Name: "src:tag3",
			},
		},
	}
	_, err = client.ImageStreamTags(testutil.Namespace()).Update(ist)
	if err == nil {
		t.Fatal("creating image stream tag should have failed")
	}
	if !quotautil.IsErrorQuotaExceeded(err) {
		t.Fatalf("expected quota exceeded error, not: %v", err)
	}

	t.Log("trying to create ImageStreamTag referencing istag already referenced")
	ist = &imageapi.ImageStreamTag{
		ObjectMeta: kapi.ObjectMeta{
			Name: "dest:tag2again",
		},
		Tag: &imageapi.TagReference{
			Name: "tag2again",
			From: &kapi.ObjectReference{
				Kind: "ImageStreamTag",
				Name: "src:tag2",
			},
		},
	}
	_, err = client.ImageStreamTags(testutil.Namespace()).Update(ist)
	if err != nil {
		t.Fatal(err)
	}
}

func TestImageStreamAdmitSpecUpdate(t *testing.T) {
	kClient, client := setupImageStreamAdmissionTest(t)

	for i, name := range []string{imagetest.BaseImageWith1LayerDigest, imagetest.BaseImageWith2LayersDigest} {
		imageReference := fmt.Sprintf("openshift/test@%s", name)
		image := &imageapi.Image{
			ObjectMeta: kapi.ObjectMeta{
				Name: name,
			},
			DockerImageReference: imageReference,
		}
		tag := fmt.Sprintf("tag%d", i+1)

		err := client.ImageStreamMappings(testutil.Namespace()).Create(&imageapi.ImageStreamMapping{
			ObjectMeta: kapi.ObjectMeta{
				Name: "src",
			},
			Tag:   tag,
			Image: *image,
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	limit := kapi.ResourceList{
		imageapi.ResourceImageStreamTags:   resource.MustParse("0"),
		imageapi.ResourceImageStreamImages: resource.MustParse("0"),
	}
	lrClient := kClient.LimitRanges(testutil.Namespace())
	createLimitRangeOfType(t, lrClient, limitRangeName, imageapi.LimitTypeImageStream, limit)

	t.Logf("trying to create a new image stream with a tag exceeding limit %v", limit)
	_, err := client.ImageStreams(testutil.Namespace()).Create(&imageapi.ImageStream{
		ObjectMeta: kapi.ObjectMeta{
			Name: "is",
		},
		Spec: imageapi.ImageStreamSpec{
			Tags: map[string]imageapi.TagReference{
				"tag1": {
					Name: "tag1",
					From: &kapi.ObjectReference{
						Kind: "ImageStreamTag",
						Name: "src:tag1",
					},
				},
			},
		},
	})
	if err == nil {
		t.Fatal("unexpected non-error")
	}
	if !quotautil.IsErrorQuotaExceeded(err) {
		t.Errorf("expected quota exceeded error, got instead: %v", err)
	}
	for _, res := range []kapi.ResourceName{imageapi.ResourceImageStreamTags, imageapi.ResourceImageStreamImages} {
		if !strings.Contains(err.Error(), string(res)) {
			t.Errorf("expected resource %q in error string: %v", res, err)
		}
	}

	limit = bumpLimit(t, lrClient, limitRangeName, imageapi.ResourceImageStreamTags, "1")
	limit = bumpLimit(t, lrClient, limitRangeName, imageapi.ResourceImageStreamImages, "1")

	t.Logf("trying to create a new image stream with a tag below limit %v", limit)
	// we may hit cache with old limit, let's retry in such a case
	err = retryOnQuotaExceeded(t, 1, func() error {
		_, err = client.ImageStreams(testutil.Namespace()).Create(&imageapi.ImageStream{
			ObjectMeta: kapi.ObjectMeta{
				Name: "is",
			},
			Spec: imageapi.ImageStreamSpec{
				Tags: map[string]imageapi.TagReference{
					"tag1": {
						Name: "tag1",
						From: &kapi.ObjectReference{
							Kind: "ImageStreamTag",
							Name: "src:tag1",
						},
					},
				},
			},
		})
		return err
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	t.Logf("adding new tag to image stream spec exceeding limit %v", limit)
	err = kclient.RetryOnConflict(kclient.DefaultRetry, func() error {
		is, err := client.ImageStreams(testutil.Namespace()).Get("is")
		if err != nil {
			return err
		}
		is.Spec.Tags["tag2"] = imageapi.TagReference{
			Name: "tag2",
			From: &kapi.ObjectReference{
				Kind: "ImageStreamTag",
				Name: "src:tag2",
			},
		}
		_, err = client.ImageStreams(testutil.Namespace()).Update(is)
		return err
	})
	if err == nil {
		t.Fatalf("unexpected non-error")
	}
	if !quotautil.IsErrorQuotaExceeded(err) {
		t.Errorf("expected quota exceeded error, got instead: %v", err)
	}
	for _, res := range []kapi.ResourceName{imageapi.ResourceImageStreamTags, imageapi.ResourceImageStreamImages} {
		if !strings.Contains(err.Error(), string(res)) {
			t.Errorf("expected resource %q in error string: %v", res, err)
		}
	}

	t.Logf("re-tagging the image under different tag")
	err = kclient.RetryOnConflict(kclient.DefaultRetry, func() error {
		is, err := client.ImageStreams(testutil.Namespace()).Get("is")
		if err != nil {
			return err
		}
		is.Spec.Tags["1again"] = imageapi.TagReference{
			Name: "1again",
			From: &kapi.ObjectReference{
				Kind: "ImageStreamTag",
				Name: "src:tag1",
			},
		}
		_, err = client.ImageStreams(testutil.Namespace()).Update(is)
		return err
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestImageStreamAdmitStatusUpdate(t *testing.T) {
	kClient, client := setupImageStreamAdmissionTest(t)
	images := []*imageapi.Image{}

	for _, name := range []string{imagetest.BaseImageWith1LayerDigest, imagetest.BaseImageWith2LayersDigest} {
		imageReference := fmt.Sprintf("openshift/test@%s", name)
		image := &imageapi.Image{
			ObjectMeta: kapi.ObjectMeta{
				Name: name,
			},
			DockerImageReference: imageReference,
		}
		images = append(images, image)

		_, err := client.Images().Create(image)
		if err != nil {
			t.Fatal(err)
		}
	}

	limit := kapi.ResourceList{
		imageapi.ResourceImageStreamTags:   resource.MustParse("0"),
		imageapi.ResourceImageStreamImages: resource.MustParse("0"),
	}
	lrClient := kClient.LimitRanges(testutil.Namespace())
	createLimitRangeOfType(t, lrClient, limitRangeName, imageapi.LimitTypeImageStream, limit)

	t.Logf("trying to create a new image stream with a tag exceeding limit %v", limit)
	_, err := client.ImageStreams(testutil.Namespace()).Create(&imageapi.ImageStream{
		ObjectMeta: kapi.ObjectMeta{
			Name: "is",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	t.Logf("adding new tag to image stream status exceeding limit %v", limit)
	err = kclient.RetryOnConflict(kclient.DefaultRetry, func() error {
		is, err := client.ImageStreams(testutil.Namespace()).Get("is")
		if err != nil {
			return err
		}
		is.Status.Tags["tag1"] = imageapi.TagEventList{
			Items: []imageapi.TagEvent{
				{
					DockerImageReference: images[0].DockerImageReference,
					Image:                images[0].Name,
				},
			},
		}
		_, err = client.ImageStreams(testutil.Namespace()).UpdateStatus(is)
		return err
	})
	if err == nil {
		t.Fatalf("unexpected non-error")
	}
	if !quotautil.IsErrorQuotaExceeded(err) {
		t.Errorf("expected quota exceeded error, got instead: %v", err)
	}
	if !strings.Contains(err.Error(), string(imageapi.ResourceImageStreamImages)) {
		t.Errorf("expected resource %q in error string: %v", imageapi.ResourceImageStreamImages, err)
	}

	limit = bumpLimit(t, lrClient, limitRangeName, imageapi.ResourceImageStreamImages, "1")

	t.Logf("adding new tag to image stream status below limit %v", limit)
	err = kclient.RetryOnConflict(kclient.DefaultRetry, func() error {
		is, err := client.ImageStreams(testutil.Namespace()).Get("is")
		if err != nil {
			return err
		}
		is.Status.Tags["tag1"] = imageapi.TagEventList{
			Items: []imageapi.TagEvent{
				{
					DockerImageReference: images[0].DockerImageReference,
					Image:                images[0].Name,
				},
			},
		}
		_, err = client.ImageStreams(testutil.Namespace()).UpdateStatus(is)
		return err
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	t.Logf("adding new tag to image stream status exceeding limit %v", limit)
	err = kclient.RetryOnConflict(kclient.DefaultRetry, func() error {
		is, err := client.ImageStreams(testutil.Namespace()).Get("is")
		if err != nil {
			return err
		}
		is.Status.Tags["tag2"] = imageapi.TagEventList{
			Items: []imageapi.TagEvent{
				{
					DockerImageReference: images[1].DockerImageReference,
					Image:                images[1].Name,
				},
			},
		}
		_, err = client.ImageStreams(testutil.Namespace()).UpdateStatus(is)
		return err
	})
	if err == nil {
		t.Fatalf("unexpected non-error")
	}
	if !quotautil.IsErrorQuotaExceeded(err) {
		t.Errorf("expected quota exceeded error, got instead: %v", err)
	}
	if !strings.Contains(err.Error(), string(imageapi.ResourceImageStreamImages)) {
		t.Errorf("expected resource %q in error string: %v", imageapi.ResourceImageStreamImages, err)
	}

	t.Logf("re-tagging the image under different tag")
	err = kclient.RetryOnConflict(kclient.DefaultRetry, func() error {
		is, err := client.ImageStreams(testutil.Namespace()).Get("is")
		if err != nil {
			return err
		}
		is.Status.Tags["1again"] = imageapi.TagEventList{
			Items: []imageapi.TagEvent{
				{
					DockerImageReference: images[0].DockerImageReference,
					Image:                images[0].Name,
				},
			},
		}
		_, err = client.ImageStreams(testutil.Namespace()).UpdateStatus(is)
		return err
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func setupImageStreamAdmissionTest(t *testing.T) (*kclient.Client, *client.Client) {
	testutil.RequireEtcd(t)

	_, clusterAdminKubeConfig, err := testserver.StartTestMasterAPI()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	kClient, err := testutil.GetClusterAdminKubeClient(clusterAdminKubeConfig)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	client, err := testutil.GetClusterAdminClient(clusterAdminKubeConfig)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	err = testutil.CreateNamespace(clusterAdminKubeConfig, testutil.Namespace())
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	_, err = client.ImageStreams(testutil.Namespace()).Create(&imageapi.ImageStream{
		ObjectMeta: kapi.ObjectMeta{
			Name: "src",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return kClient, client
}

// createResourceQuota creates a resource quota with given hard limits in a current namespace and waits until
// a first usage refresh
func createResourceQuota(t *testing.T, rqClient kclient.ResourceQuotaInterface, quotaName string, hard kapi.ResourceList) *kapi.ResourceQuota {
	rq := &kapi.ResourceQuota{
		ObjectMeta: kapi.ObjectMeta{
			Name: quotaName,
		},
		Spec: kapi.ResourceQuotaSpec{
			Hard: hard,
		},
	}

	t.Logf("creating resource quota %q with a limit %v", quotaName, hard)
	rq, err := rqClient.Create(rq)
	if err != nil {
		t.Fatal(err)
	}
	err = testutil.WaitForResourceQuotaLimitSync(rqClient, quotaName, hard, time.Second*30)
	if err != nil {
		t.Fatal(err)
	}
	return rq
}

// bumpQuota modifies hard spec of quota object with the given value. It returns modified hard spec.
func bumpQuota(t *testing.T, rqs kclient.ResourceQuotaInterface, quotaName string, resourceName kapi.ResourceName, value int64) kapi.ResourceList {
	t.Logf("bump the quota %s to %s=%d", quotaName, resourceName, value)
	rq, err := rqs.Get(quotaName)
	if err != nil {
		t.Fatal(err)
	}
	rq.Spec.Hard[resourceName] = *resource.NewQuantity(value, resource.DecimalSI)
	_, err = rqs.Update(rq)
	if err != nil {
		t.Fatal(err)
	}
	err = testutil.WaitForResourceQuotaLimitSync(
		rqs,
		quotaName,
		rq.Spec.Hard,
		time.Second*10)
	if err != nil {
		t.Fatal(err)
	}
	return rq.Spec.Hard
}

// createLimitRangeOfType creates a new limit range object with given max limits set for given limit type. The
// object will be created in current namespace.
func createLimitRangeOfType(t *testing.T, lrClient kclient.LimitRangeInterface, limitRangeName string, limitType kapi.LimitType, maxLimits kapi.ResourceList) *kapi.LimitRange {
	lr := &kapi.LimitRange{
		ObjectMeta: kapi.ObjectMeta{
			Name: limitRangeName,
		},
		Spec: kapi.LimitRangeSpec{
			Limits: []kapi.LimitRangeItem{
				{
					Type: limitType,
					Max:  maxLimits,
				},
			},
		},
	}

	t.Logf("creating limit range object %q with %s limited to: %v", limitRangeName, limitType, maxLimits)
	lr, err := lrClient.Create(lr)
	if err != nil {
		t.Fatal(err)
	}
	return lr
}

func bumpLimit(t *testing.T, lrClient kclient.LimitRangeInterface, limitRangeName string, resourceName kapi.ResourceName, limit string) kapi.ResourceList {
	t.Logf("bump a limit on resource %q to %s", resourceName, limit)
	lr, err := lrClient.Get(limitRangeName)
	if err != nil {
		t.Fatal(err)
	}
	res := kapi.ResourceList{}

	change := false
	for i := range lr.Spec.Limits {
		item := &lr.Spec.Limits[i]
		if old, exists := item.Max[resourceName]; exists {
			for k, v := range item.Max {
				res[k] = v
			}
			parsed := resource.MustParse(limit)
			if old.Cmp(parsed) != 0 {
				item.Max[resourceName] = parsed
				change = true
			}
		}
	}

	if !change {
		return res
	}
	_, err = lrClient.Update(lr)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func retryOnQuotaExceeded(t *testing.T, retries int, fn func() error) error {
	var err error
	for i := 0; i < retries; i++ {
		err = fn()
		if !quotautil.IsErrorQuotaExceeded(err) {
			return err
		}
		t.Logf("got quota exceeded error, retrying after sleep; number of retries remaining: %d", retries-i-1)
		time.Sleep(time.Second)
	}
	return err
}