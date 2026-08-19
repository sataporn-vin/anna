#!/usr/bin/env ruby

require "yaml"

schema_path = ARGV.fetch(0, "chatgpt-actions.openapi.yaml")
document = YAML.safe_load(File.read(schema_path), [], [], true, schema_path)
errors = []

document.fetch("components", {}).fetch("schemas", {}).each do |name, schema|
  next unless schema.is_a?(Hash) && schema["type"] == "object"
  next if schema["properties"].is_a?(Hash) && !schema["properties"].empty?

  errors << "In context=('components', 'schemas', '#{name}'), object schema missing properties"
end

http_methods = %w[get put post delete options head patch trace].freeze
document.fetch("paths", {}).each do |path, path_item|
  next unless path_item.is_a?(Hash)

  path_item.each_key do |key|
    next if http_methods.include?(key) || key.start_with?("x-")

    errors << "Path #{path} has unrecognized method #{key}; skipping"
  end

  path_item.each do |method, operation|
    next unless http_methods.include?(method) && operation.is_a?(Hash)

    operation_errors = operation.fetch("parameters", []).each_with_object([]) do |parameter, result|
      next if parameter.is_a?(Hash) && parameter["name"].is_a?(String)

      result << "In path #{path}, method #{method}, operationId #{operation["operationId"]}, parameter #{parameter.inspect} is has missing or non-string name; skipping"
    end
    errors.concat(operation_errors)
    if operation_errors.any?
      errors << "In path #{path}, method #{method}, operationId #{operation["operationId"]}, skipping function due to errors"
    end
  end
end

if errors.empty?
  puts "Known ChatGPT Actions compatibility checks passed"
  exit 0
end

warn errors.join("\n")
exit 1
