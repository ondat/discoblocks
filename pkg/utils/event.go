package utils

import (
	"context"
	"fmt"
	"math"
	"time"

	ttlcache "github.com/jellydator/ttlcache/v3"
	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	eventTimeout = time.Minute
	cacheTimeout = 5 * time.Second
)

// EventService main interface of event service
type EventService interface {
	SendWarning(string, string, string, string, string, client.Object, client.Object) error
	SendNormal(string, string, string, string, string, client.Object, client.Object) error
}

// eventService sends events to Kubernetes
type eventService struct {
	ControllerInstance string
	Client             client.Client

	eventCache *ttlcache.Cache[string, bool]
}

// Send sends a warning event to Kubernetes in the background
func (es *eventService) SendWarning(namespace, instance, action, reason, note string, regarding, related client.Object) error {
	return es.send("Warning", namespace, instance, action, reason, note, regarding, related)
}

// Send sends a normal event to Kubernetes in the background
func (es *eventService) SendNormal(namespace, instance, action, reason, note string, regarding, related client.Object) error {
	return es.send("Normal", namespace, instance, action, reason, note, regarding, related)
}

func (es *eventService) send(eventType, namespace, instance, action, reason, note string, regarding, related client.Object) error {
	relatedUID := ""
	if related != nil {
		relatedUID = string(related.GetUID())
	}

	hash, err := RenderResourceName(true, eventType, namespace, es.ControllerInstance, instance, action, reason, note, string(regarding.GetUID()), relatedUID)
	if err != nil {
		return fmt.Errorf("unable to render event hash: %w", err)
	}

	now := time.Now()

	name, err := RenderResourceName(true, es.ControllerInstance, action, string(regarding.GetUID()), now.String())
	if err != nil {
		return fmt.Errorf("unable to render event name: %w", err)
	}

	event := eventsv1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Type:                eventType,
		EventTime:           metav1.NewMicroTime(now),
		ReportingController: es.ControllerInstance,
		ReportingInstance:   instance,
		Action:              action,
		Reason:              reason,
		Note:                note,
		Regarding: corev1.ObjectReference{
			Kind:      regarding.GetObjectKind().GroupVersionKind().Kind,
			Namespace: regarding.GetNamespace(),
			Name:      regarding.GetName(),
			UID:       regarding.GetUID(),
		},
	}

	if related != nil {
		event.Related = &corev1.ObjectReference{
			Kind:      related.GetObjectKind().GroupVersionKind().Kind,
			Namespace: related.GetNamespace(),
			Name:      related.GetName(),
			UID:       related.GetUID(),
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), eventTimeout)
	defer cancel()

	lastMessage := es.eventCache.Get(hash)
	if lastMessage != nil && !lastMessage.IsExpired() {
		return nil
	}

	es.eventCache.Set(hash, true, ttlcache.DefaultTTL)

	if err := es.Client.Create(ctx, &event); err != nil {
		es.eventCache.Delete(hash)

		return fmt.Errorf("unable to create event: %w", err)
	}

	return nil
}

// NewEventService creates a new event service
func NewEventService(controllerID string, k8sClient client.Client) EventService {
	return &eventService{
		ControllerInstance: controllerID,
		Client:             k8sClient,

		eventCache: ttlcache.New(
			ttlcache.WithTTL[string, bool](cacheTimeout),
			ttlcache.WithDisableTouchOnHit[string, bool](),
			ttlcache.WithCapacity[string, bool](math.MaxUint16),
		),
	}
}
