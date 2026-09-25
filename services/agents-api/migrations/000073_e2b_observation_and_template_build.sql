-- +goose Up
-- Providers such as E2B report a current CPU share instead of cumulative time.
ALTER TABLE runtime_history_samples ADD COLUMN cpu_utilization_ratio double precision
    CHECK (cpu_utilization_ratio >= 0 AND cpu_utilization_ratio < 'Infinity'::double precision);
ALTER TABLE runtime_history_samples ADD CONSTRAINT runtime_history_samples_cpu_utilization_incarnation
    CHECK (started_at_ns IS NOT NULL OR cpu_utilization_ratio IS NULL);

-- The fixed E2B template build as read by the validation that admitted the
-- selection. Selections saved before these columns existed keep nulls.
ALTER TABLE runtime_deployment ADD COLUMN e2b_template_build_status text CHECK (e2b_template_build_status <> '');
ALTER TABLE runtime_deployment ADD COLUMN e2b_template_cpus integer CHECK (e2b_template_cpus > 0);
ALTER TABLE runtime_deployment ADD COLUMN e2b_template_memory_mib integer CHECK (e2b_template_memory_mib > 0);
ALTER TABLE runtime_deployment ADD COLUMN e2b_template_root_disk_mib integer CHECK (e2b_template_root_disk_mib > 0);
ALTER TABLE runtime_deployment ADD CONSTRAINT runtime_deployment_e2b_template_build_check CHECK (
    provider_kind = 'e2b' OR (e2b_template_build_status IS NULL AND e2b_template_cpus IS NULL AND
        e2b_template_memory_mib IS NULL AND e2b_template_root_disk_mib IS NULL)
);

-- +goose Down
ALTER TABLE runtime_deployment DROP CONSTRAINT runtime_deployment_e2b_template_build_check;
ALTER TABLE runtime_deployment DROP COLUMN e2b_template_root_disk_mib;
ALTER TABLE runtime_deployment DROP COLUMN e2b_template_memory_mib;
ALTER TABLE runtime_deployment DROP COLUMN e2b_template_cpus;
ALTER TABLE runtime_deployment DROP COLUMN e2b_template_build_status;
ALTER TABLE runtime_history_samples DROP CONSTRAINT runtime_history_samples_cpu_utilization_incarnation;
ALTER TABLE runtime_history_samples DROP COLUMN cpu_utilization_ratio;
