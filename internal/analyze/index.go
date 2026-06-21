package analyze

import "github.com/yourname/acg/internal/model"

// index — заранее построенные карты по нормализованному снапшоту.
// Цель: O(1)-доступ внутри Inbound, чтобы при обходе всех destination'ов
// не пересобирать срезы и не делать линейный поиск на каждом шаге.
//
// Индексы держит analyze, а не model (AP-2): модель — это данные, обращение к ним — забота движка.
type index struct {
	workload map[int64]*model.Workload          // workloadID -> workload
	service  map[int64]*model.Service           // serviceID  -> service
	nsByID   map[int64]*model.Namespace         // namespaceID -> namespace (для имени)
	saByID   map[int64]*model.ServiceAccount    // saID -> SA (для имени)
	policiesInNS map[int64][]*model.AuthorizationPolicy // namespaceID -> политики этого namespace

	// servicesByWorkload материализует обратную сторону service_workload_match:
	// для destination-workload — какие сервисы в него ведут (его адресуемость).
	servicesByWorkload map[int64][]*model.Service
}

func buildIndex(ns *model.NormalizedSnapshot) *index {
	idx := &index{
		workload:           make(map[int64]*model.Workload, len(ns.Workloads)),
		service:            make(map[int64]*model.Service, len(ns.Services)),
		nsByID:             make(map[int64]*model.Namespace, len(ns.Namespaces)),
		saByID:             make(map[int64]*model.ServiceAccount, len(ns.ServiceAccounts)),
		policiesInNS:       make(map[int64][]*model.AuthorizationPolicy),
		servicesByWorkload: make(map[int64][]*model.Service),
	}

	for _, w := range ns.Workloads {
		idx.workload[w.ID] = w
	}
	for _, s := range ns.Services {
		idx.service[s.ID] = s
	}
	for _, n := range ns.Namespaces {
		idx.nsByID[n.ID] = n
	}
	for _, sa := range ns.ServiceAccounts {
		idx.saByID[sa.ID] = sa
	}
	for _, p := range ns.AuthPolicies {
		idx.policiesInNS[p.NamespaceID] = append(idx.policiesInNS[p.NamespaceID], p)
	}

	// Разворачиваем service_workload_match в карту "сервисы по workload'у".
	// Matches уже в детерминированном порядке (normalize строит его по отсортированным срезам),
	// поэтому и срезы здесь стабильны — не нужно пересортировывать.
	for _, m := range ns.Matches {
		if svc := idx.service[m.ServiceID]; svc != nil {
			idx.servicesByWorkload[m.WorkloadID] = append(idx.servicesByWorkload[m.WorkloadID], svc)
		}
	}

	return idx
}

// servicesForWorkload — сервисы, через которые адресуется данный workload (его «входы»).
func (i *index) servicesForWorkload(workloadID int64) []*model.Service {
	return i.servicesByWorkload[workloadID]
}

func (i *index) nsName(nsID int64) string {
	if n := i.nsByID[nsID]; n != nil {
		return n.Name
	}
	return ""
}

func (i *index) saName(saID int64) string {
	if sa := i.saByID[saID]; sa != nil {
		return sa.Name
	}
	return ""
}
