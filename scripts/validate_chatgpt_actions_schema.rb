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
end

if errors.empty?
  puts "Known ChatGPT Actions compatibility checks passed"
  exit 0
end

warn errors.join("\n")
exit 1
