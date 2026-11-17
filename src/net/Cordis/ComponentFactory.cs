// -----------------------------------------------------------------------
// <copyright file="ComponentFactory.cs" company="Smartpcr">
//     Copyright (c) Smartpcr. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

using System.Reflection;
using System.Runtime.ExceptionServices;

internal static class ComponentFactory
{
    public static ComponentRuntime ForType(Type type)
    {
        var schema = FindSchema(type);
        var name = type.Name;
        return new ComponentRuntime(
            type,
            name,
            (ctx, config) =>
            {
                var ctor = SelectConstructor(type, config is not null);
                var parameters = ctor.GetParameters();
                var args = parameters.Length switch
                {
                    0 => [],
                    1 => [ctx],
                    _ => new object?[] { ctx, config ?? DefaultFor(parameters[1]) },
                };
                try
                {
                    return new ValueTask<object?>(ctor.Invoke(args));
                }
                catch (TargetInvocationException error) when (error.InnerException is not null)
                {
                    ExceptionDispatchInfo.Capture(error.InnerException).Throw();
                    throw;
                }
            },
            schema);
    }

    public static ComponentRuntime ForInstance(IComponent component)
    {
        var name = component.ComponentName ?? component.GetType().Name;
        return new ComponentRuntime(
            component,
            name,
            async (ctx, config) =>
            {
                await InvokeAsync(component, ctx, config).ConfigureAwait(false);
                return null;
            },
            component.ConfigSchema);
    }

    public static ComponentRuntime ForDelegate(object key, string name, Func<Context, ValueTask> apply)
        => new(
            key,
            name,
            async (ctx, _) =>
            {
                await apply(ctx).ConfigureAwait(false);
                return null;
            },
            null);

    /// <summary>Reads <c>[Inject]</c> attributes declared on a component type (inherited).</summary>
    /// <param name="type">The component type.</param>
    /// <returns>The declared service names, each mapped to its per-dependency config.</returns>
    public static Dictionary<string, object?> InjectOf(Type type)
    {
        var result = new Dictionary<string, object?>(StringComparer.Ordinal);
        foreach (var attribute in type.GetCustomAttributes<InjectAttribute>(inherit: true))
        {
            result[attribute.Name] = attribute.Config;
        }

        return result;
    }

    public static Dictionary<string, object?> InjectOf(IEnumerable<string> names)
    {
        var result = new Dictionary<string, object?>(StringComparer.Ordinal);
        foreach (var name in names)
        {
            result[name] = null;
        }

        return result;
    }

    /// <summary>Looks for <c>public static IConfigSchema Schema { get; }</c> on the component type.</summary>
    /// <param name="type">The component type.</param>
    /// <returns>The schema exposed by the public static <c>Schema</c> property or field, or <see langword="null"/> if there is none.</returns>
    internal static IConfigSchema? FindSchema(Type type)
    {
        var property = type.GetProperty("Schema", BindingFlags.Public | BindingFlags.Static | BindingFlags.FlattenHierarchy);
        if (property is not null && typeof(IConfigSchema).IsAssignableFrom(property.PropertyType))
        {
            return (IConfigSchema?)property.GetValue(null);
        }

        var field = type.GetField("Schema", BindingFlags.Public | BindingFlags.Static | BindingFlags.FlattenHierarchy);
        if (field is not null && typeof(IConfigSchema).IsAssignableFrom(field.FieldType))
        {
            return (IConfigSchema?)field.GetValue(null);
        }

        return null;
    }

    private static ValueTask InvokeAsync(IComponent component, Context ctx, object? config)
    {
        var closed = component.GetType().GetInterfaces()
            .FirstOrDefault(i => i.IsGenericType && i.GetGenericTypeDefinition() == typeof(IComponent<>));
        if (closed is null || config is null)
        {
            return component.ApplyAsync(ctx);
        }

        var method = closed.GetMethod(nameof(IComponent<object>.ApplyAsync), [typeof(Context), closed.GetGenericArguments()[0]])!;
        try
        {
            return (ValueTask)method.Invoke(component, [ctx, config])!;
        }
        catch (TargetInvocationException error) when (error.InnerException is not null)
        {
            ExceptionDispatchInfo.Capture(error.InnerException).Throw();
            throw;
        }
    }

    private static ConstructorInfo SelectConstructor(Type type, bool hasConfig)
    {
        var ctors = type.GetConstructors(BindingFlags.Public | BindingFlags.Instance);
        var withConfig = ctors.FirstOrDefault(c =>
            c.GetParameters().Length == 2 && c.GetParameters()[0].ParameterType == typeof(Context));
        var ctxOnly = ctors.FirstOrDefault(c =>
            c.GetParameters().Length == 1 && c.GetParameters()[0].ParameterType == typeof(Context));

        var selected = hasConfig ? withConfig ?? ctxOnly : ctxOnly ?? withConfig;
        selected ??= ctors.FirstOrDefault(c => c.GetParameters().Length == 0);

        return selected ?? throw new CordisException(
            $"component type {type.Name} needs a constructor taking (Context) or (Context, TConfig)");
    }

    private static object? DefaultFor(ParameterInfo parameter)
    {
        if (parameter.HasDefaultValue)
        {
            return parameter.DefaultValue;
        }

        var type = parameter.ParameterType;
        if (type.IsValueType)
        {
            return Activator.CreateInstance(type);
        }

        if (type.GetConstructor(Type.EmptyTypes) is not null)
        {
            return Activator.CreateInstance(type);
        }

        return null;
    }
}
