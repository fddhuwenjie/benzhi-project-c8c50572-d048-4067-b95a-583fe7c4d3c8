package persistence

import (
	"encoding/json"
	"ground-clock-qualification/internal/domain"
)

func (s *Store) ReferencesByDigest(digest string) ([]domain.ReferenceEvidence, error) {
	rows, err := s.db.Query(`SELECT data FROM refs WHERE lower(json_extract(data,'$.certificate_digest'))=lower(?) ORDER BY json_extract(data,'$.submitted_at'),campaign_id,id`, digest)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.ReferenceEvidence{}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var item domain.ReferenceEvidence
		if err = json.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) AllReferenceWithdrawals() ([]domain.ReferenceWithdrawal, error) {
	rows, err := s.db.Query(`SELECT data FROM reference_withdrawals ORDER BY json_extract(data,'$.withdrawn_at'),campaign_id,evidence_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.ReferenceWithdrawal{}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var item domain.ReferenceWithdrawal
		if err = json.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) RemediationDependencies(cid string) ([]domain.RemediationDependency, error) {
	rows, err := s.db.Query(`SELECT data FROM remediation_dependencies WHERE campaign_id=? ORDER BY version,deviation_id,prerequisite_id`, cid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.RemediationDependency{}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var item domain.RemediationDependency
		if err = json.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
