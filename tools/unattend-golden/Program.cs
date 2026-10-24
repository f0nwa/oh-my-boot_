// Runs the original Schneegans generator on settings written as JSON, so the
// Go port in internal/unattend can be compared with it byte for byte.
//
// JSON format (the same the Go port reads):
//   - the root object holds Configuration properties to change from
//     Configuration.Default, by their C# names;
//   - a value of an interface or abstract type is an object with "$type", the
//     exact C# class name, and that class's constructor parameters;
//   - a keyed lookup (ImageLanguage, UserLocale, Bloatware, …) is its id;
//   - enums are names, byte arrays base64, dictionaries arrays of
//     {"Key": …, "Value": …}.
//
// Usage: dotnet run -- a.json [b.json …]   writes a.xml, or a.err with the
// ConfigurationException message.
using System;
using System.Collections;
using System.Collections.Generic;
using System.Collections.Immutable;
using System.IO;
using System.Linq;
using System.Reflection;
using Newtonsoft.Json.Linq;

namespace Schneegans.Unattend;

static class GoldenProgram
{
  static readonly UnattendGenerator Generator = new();
  static readonly Type[] AllTypes = typeof(Configuration).Assembly.GetTypes();

  public static int Main(string[] args)
  {
    int failed = 0;
    foreach (string path in args)
    {
      string stem = Path.ChangeExtension(path, null);
      File.Delete(stem + ".xml");
      File.Delete(stem + ".err");
      try
      {
        var config = Build(JObject.Parse(File.ReadAllText(path)));
        File.WriteAllBytes(stem + ".xml", UnattendGenerator.Serialize(Generator.GenerateXml(config)));
      }
      catch (Exception e) when (Unwrap(e) is ConfigurationException ce)
      {
        File.WriteAllText(stem + ".err", ce.Message + "\n");
      }
      catch (Exception e)
      {
        Console.Error.WriteLine($"{path}: {Unwrap(e)}");
        failed++;
      }
    }
    return failed == 0 ? 0 : 1;
  }

  static Exception Unwrap(Exception e) => e is TargetInvocationException { InnerException: { } inner } ? Unwrap(inner) : e;

  static Configuration Build(JObject root)
  {
    var config = Configuration.Default;
    var clone = typeof(Configuration).GetMethod("<Clone>$")!;
    config = (Configuration)clone.Invoke(config, null)!;
    foreach (var prop in root.Properties())
    {
      var info = typeof(Configuration).GetProperty(prop.Name) ?? throw new ArgumentException($"unknown setting '{prop.Name}'");
      info.SetValue(config, Convert(info.PropertyType, prop.Value));
    }
    return config;
  }

  static object? Convert(Type t, JToken tok)
  {
    if (tok.Type == JTokenType.Null)
    {
      return null;
    }
    var nullable = Nullable.GetUnderlyingType(t);
    if (nullable != null)
    {
      return Convert(nullable, tok);
    }
    if (t == typeof(string)) return tok.Value<string>();
    if (t == typeof(bool)) return tok.Value<bool>();
    if (t == typeof(int)) return tok.Value<int>();
    if (t == typeof(long)) return tok.Value<long>();
    if (t == typeof(double)) return tok.Value<double>();
    if (t == typeof(byte[])) return System.Convert.FromBase64String(tok.Value<string>()!);
    if (t.IsEnum) return Enum.Parse(t, tok.Value<string>()!);
    if (t == typeof(System.Drawing.Color)) return System.Drawing.ColorTranslator.FromHtml(tok.Value<string>()!);
    if (typeof(IKeyed).IsAssignableFrom(t) && tok.Type == JTokenType.String)
    {
      return LookupKeyed(t, tok.Value<string>()!);
    }
    if (t.IsGenericType)
    {
      var def = t.GetGenericTypeDefinition();
      var args = t.GetGenericArguments();
      if (args.Length == 2 && (def == typeof(ImmutableDictionary<,>) || def == typeof(IImmutableDictionary<,>) || def == typeof(IDictionary<,>) || def == typeof(Dictionary<,>)))
      {
        var builder = typeof(ImmutableDictionary).GetMethod(nameof(ImmutableDictionary.CreateBuilder), Type.EmptyTypes)!.MakeGenericMethod(args).Invoke(null, null)!;
        var add = builder.GetType().GetMethod("Add", args)!;
        foreach (var item in (JArray)tok)
        {
          add.Invoke(builder, [Convert(args[0], item["Key"]!), Convert(args[1], item["Value"]!)]);
        }
        return builder.GetType().GetMethod("ToImmutable")!.Invoke(builder, null);
      }
      if (args.Length == 1 && typeof(IEnumerable).IsAssignableFrom(t))
      {
        var list = (IList)Activator.CreateInstance(typeof(List<>).MakeGenericType(args))!;
        foreach (var item in (JArray)tok)
        {
          list.Add(Convert(args[0], item));
        }
        if (def == typeof(ImmutableHashSet<>) || def == typeof(IImmutableSet<>) || def == typeof(ISet<>))
        {
          return typeof(ImmutableHashSet).GetMethods().First(m => m.Name == "CreateRange" && m.GetParameters().Length == 1).MakeGenericMethod(args).Invoke(null, [list]);
        }
        if (def == typeof(ImmutableList<>) || def == typeof(IImmutableList<>))
        {
          return typeof(ImmutableList).GetMethods().First(m => m.Name == "CreateRange" && m.GetParameters().Length == 1).MakeGenericMethod(args).Invoke(null, [list]);
        }
        if (def == typeof(ImmutableArray<>))
        {
          return typeof(ImmutableArray).GetMethods().First(m => m.Name == "CreateRange" && m.GetParameters().Length == 1 && m.GetParameters()[0].ParameterType.IsGenericType).MakeGenericMethod(args).Invoke(null, [list]);
        }
        return list;
      }
    }
    if (t.IsArray)
    {
      var items = ((JArray)tok).Select(item => Convert(t.GetElementType()!, item)).ToArray();
      var array = Array.CreateInstance(t.GetElementType()!, items.Length);
      Array.Copy(items, array, items.Length);
      return array;
    }
    if (tok is not JObject obj)
    {
      throw new ArgumentException($"expected an object for {t.Name}, got {tok.Type}");
    }
    var concrete = t;
    if (t.IsInterface || t.IsAbstract || obj["$type"] != null)
    {
      string name = obj["$type"]?.Value<string>() ?? throw new ArgumentException($"'$type' needed for {t.Name}");
      concrete = AllTypes.FirstOrDefault(c => c.Name == name && t.IsAssignableFrom(c) && !c.IsAbstract) ?? throw new ArgumentException($"no type '{name}' for {t.Name}");
    }
    return Construct(concrete, obj);
  }

  static object Construct(Type t, JObject obj)
  {
    var ctor = t.GetConstructors().OrderByDescending(c => c.GetParameters().Length).First();
    var given = obj.Properties().Where(p => p.Name != "$type").ToDictionary(p => p.Name, p => p.Value, StringComparer.OrdinalIgnoreCase);
    var parameters = ctor.GetParameters();
    var values = new object?[parameters.Length];
    for (int i = 0; i < parameters.Length; i++)
    {
      var p = parameters[i];
      if (given.Remove(p.Name!, out var value))
      {
        values[i] = Convert(p.ParameterType, value);
      }
      else if (p.HasDefaultValue)
      {
        values[i] = p.DefaultValue;
      }
      else if (new System.Reflection.NullabilityInfoContext().Create(p).WriteState == System.Reflection.NullabilityState.Nullable)
      {
        // A nullable parameter left out of JSON is null, as in Go.
        values[i] = null;
      }
      else
      {
        throw new ArgumentException($"{t.Name}: missing '{p.Name}'");
      }
    }
    var result = ctor.Invoke(values);
    // Properties set with initializers rather than constructor parameters.
    foreach (var (name, value) in given)
    {
      var info = t.GetProperty(name, BindingFlags.Public | BindingFlags.Instance | BindingFlags.IgnoreCase) ?? throw new ArgumentException($"{t.Name}: unknown '{name}'");
      info.SetValue(result, Convert(info.PropertyType, value));
    }
    return result;
  }

  static object LookupKeyed(Type t, string key)
  {
    var dictType = typeof(IImmutableDictionary<,>).MakeGenericType(typeof(string), t);
    var prop = typeof(UnattendGenerator).GetProperties().FirstOrDefault(p => dictType.IsAssignableFrom(p.PropertyType)) ?? throw new ArgumentException($"no lookup for {t.Name}");
    var dict = (IDictionary)prop.GetValue(Generator)!;
    if (!dict.Contains(key))
    {
      throw new ConfigurationException($"Could not find an element of type '{t.Name}' with key '{key}'.");
    }
    return dict[key]!;
  }
}
